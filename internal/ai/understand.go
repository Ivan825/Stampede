package ai

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/tidwall/gjson"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// Inputs are what a user gives the generator. At least one is required.
type Inputs struct {
	// Description is plain language: who the users are and what they do.
	Description string
	// OpenAPI is an OpenAPI 3.x document (YAML or JSON).
	OpenAPI []byte
	// HAR is a browser or proxy recording (HTTP Archive 1.2).
	HAR []byte
	// GraphQL is a GraphQL schema: an introspection result (JSON) or SDL.
	GraphQL []byte
	// GraphQLPath is where the GraphQL API is served (default /graphql).
	GraphQLPath string
	// AccessLog is a web server access log (common, combined or any format
	// with a quoted "METHOD /path" request line).
	AccessLog []byte
	// Existing is a scenario to compare the proposal with.
	Existing []byte
	// Proto holds .proto sources by file name (the name imports use). The
	// generator writes grpc steps for their services' methods.
	Proto map[string][]byte
	// ProtoPaths, when set, are the paths grpc steps name in proto: so a
	// run loads the same descriptors (the CLI passes the files it read).
	// Without them steps rely on the server's reflection service.
	ProtoPaths []string
	// ProtoImportPaths are directories where imports of the .proto files
	// that are not in Proto are found (CLI only; steps name them in
	// importPaths).
	ProtoImportPaths []string
}

// Endpoint is one operation of the system under test.
type Endpoint struct {
	Method string `json:"method"`
	// Path uses {name} for parameters, as in OpenAPI.
	Path    string `json:"path"`
	Summary string `json:"summary,omitempty"`
	// Auth is set when the endpoint needs credentials.
	Auth bool `json:"auth,omitempty"`
	// Source is openapi, har or log.
	Source string `json:"source"`
	// Count is how often the endpoint appeared in recorded traffic.
	Count int `json:"count,omitempty"`
}

// Key is "METHOD /path".
func (e Endpoint) Key() string { return e.Method + " " + e.Path }

// Dependency says one call produces a value another call needs.
type Dependency struct {
	Producer string `json:"producer"` // "POST /api/login"
	Value    string `json:"value"`    // "$.token", "cookie session", "header Location"
	Consumer string `json:"consumer"` // "GET /api/me"
	Via      string `json:"via"`      // "Authorization: Bearer", "path {id}", "json productId"
	Kind     string `json:"kind"`     // token, id, cookie
}

// MixEntry is an endpoint's share of logged traffic.
type MixEntry struct {
	Endpoint string  `json:"endpoint"`
	Count    int     `json:"count"`
	Share    float64 `json:"share"`
}

// VisitPattern is a sequence of endpoints seen in one client visit.
type VisitPattern struct {
	Steps []string `json:"steps"`
	Count int      `json:"count"`
	Share float64  `json:"share"`
}

// Understanding is the result of the first pipeline stage.
type Understanding struct {
	Endpoints    []Endpoint     `json:"endpoints"`
	Dependencies []Dependency   `json:"dependencies"`
	Mix          []MixEntry     `json:"mix,omitempty"`
	Visits       []VisitPattern `json:"visits,omitempty"`
	// HasSpec is set when endpoints come from OpenAPI or a HAR, so the
	// static check can require every request to use a known endpoint.
	HasSpec bool `json:"hasSpec"`
	// BasePath is the path of the spec's first server URL ("/api/v1"),
	// which prefixes every spec path on the wire.
	BasePath string `json:"basePath,omitempty"`
	// GRPCMethods are the methods of the .proto inputs; ProtoFiles their
	// compiled descriptors, which the dry run uses.
	GRPCMethods []GRPCMethod         `json:"grpcMethods,omitempty"`
	ProtoFiles  *protoregistry.Files `json:"-"`
	// Context is the redacted text the model sees.
	Context string `json:"-"`
}

// Limits on what is sent to a model.
const (
	maxDescription   = 20_000
	maxSpecDigest    = 60_000
	maxHARDigest     = 30_000
	maxHAREntries    = 60
	maxLogLines      = 500_000
	sampleBodyBytes  = 600
	maxDependencies  = 60
	maxVisitPatterns = 12
)

// Understand builds the dependency map and the model's context from the
// inputs. Traffic is redacted with red; documents lose credentials only.
func Understand(in Inputs, red *Redactor) (*Understanding, error) {
	if strings.TrimSpace(in.Description) == "" && len(in.OpenAPI) == 0 && len(in.HAR) == 0 && len(in.AccessLog) == 0 && len(in.GraphQL) == 0 && len(in.Proto) == 0 {
		return nil, errors.New("give at least one input: a description, an OpenAPI spec, a GraphQL schema, .proto files, a HAR file or an access log")
	}
	u := &Understanding{}
	var ctx strings.Builder
	if d := strings.TrimSpace(in.Description); d != "" {
		ctx.WriteString("## What the user wants\n\n")
		ctx.WriteString(Truncate(red.Secrets(d), maxDescription))
		ctx.WriteString("\n\n")
	}
	if len(in.OpenAPI) > 0 {
		digest, err := u.fromOpenAPI(in.OpenAPI, red)
		if err != nil {
			return nil, err
		}
		ctx.WriteString(digest)
	}
	if len(in.GraphQL) > 0 {
		digest, err := u.fromGraphQL(in.GraphQL, in.GraphQLPath)
		if err != nil {
			return nil, err
		}
		ctx.WriteString(digest)
	}
	if len(in.Proto) > 0 {
		digest, err := u.fromProto(in.Proto, in.ProtoPaths, in.ProtoImportPaths)
		if err != nil {
			return nil, err
		}
		ctx.WriteString(digest)
	}
	if len(in.HAR) > 0 {
		digest, err := u.fromHAR(in.HAR, red)
		if err != nil {
			return nil, err
		}
		ctx.WriteString(digest)
	}
	if len(in.AccessLog) > 0 {
		digest, err := u.fromLog(in.AccessLog)
		if err != nil {
			return nil, err
		}
		ctx.WriteString(digest)
	}
	if len(u.Dependencies) > maxDependencies {
		u.Dependencies = u.Dependencies[:maxDependencies]
	}
	if len(u.Dependencies) > 0 {
		ctx.WriteString("## Dependency map\n\nValues one call produces that another call needs. Extract them into variables before use.\n\n")
		for _, d := range u.Dependencies {
			fmt.Fprintf(&ctx, "- %s → %s → %s (%s)\n", d.Producer, d.Value, d.Consumer, d.Via)
		}
		ctx.WriteString("\n")
	}
	u.Context = ctx.String()
	return u, nil
}

func (u *Understanding) addEndpoint(e Endpoint) {
	for i := range u.Endpoints {
		if u.Endpoints[i].Method == e.Method && u.Endpoints[i].Path == e.Path {
			u.Endpoints[i].Count += e.Count
			if u.Endpoints[i].Summary == "" {
				u.Endpoints[i].Summary = e.Summary
			}
			u.Endpoints[i].Auth = u.Endpoints[i].Auth || e.Auth
			return
		}
	}
	u.Endpoints = append(u.Endpoints, e)
}

func (u *Understanding) addDependency(d Dependency) {
	for _, x := range u.Dependencies {
		if x.Producer == d.Producer && x.Consumer == d.Consumer && x.Via == d.Via {
			return
		}
	}
	u.Dependencies = append(u.Dependencies, d)
}

// OpenAPI.

type producer struct {
	op       string
	path     string // JSONPath of the value
	field    string
	resource string // singular resource name for ids
	kind     string
}

func (u *Understanding) fromOpenAPI(src []byte, red *Redactor) (string, error) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	doc, err := loader.LoadFromData(src)
	if err != nil {
		return "", fmt.Errorf("read OpenAPI spec: %w", err)
	}
	if doc.Paths == nil || doc.Paths.Len() == 0 {
		return "", errors.New("the OpenAPI spec has no paths")
	}
	u.HasSpec = true
	if len(doc.Servers) > 0 && doc.Servers[0] != nil {
		if su, err := url.Parse(doc.Servers[0].URL); err == nil && !strings.Contains(su.Path, "{") {
			u.BasePath = strings.TrimRight(su.Path, "/")
		}
	}

	var b strings.Builder
	b.WriteString("## API endpoints (from the OpenAPI spec)\n\n")
	if u.BasePath != "" {
		fmt.Fprintf(&b, "Every path below is relative to %s; include that prefix in request paths.\n\n", u.BasePath)
	}
	if doc.Info != nil {
		if doc.Info.Title != "" {
			fmt.Fprintf(&b, "API: %s\n", doc.Info.Title)
		}
		if doc.Info.Description != "" {
			b.WriteString(Truncate(red.Secrets(strings.TrimSpace(doc.Info.Description)), 4000))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	globalAuth := len(doc.Security) > 0

	paths := doc.Paths.InMatchingOrder()
	sort.Strings(paths)
	var producers []producer
	type consumer struct {
		op, resource, via, kind string
	}
	var consumers []consumer

	for _, p := range paths {
		item := doc.Paths.Value(p)
		ops := item.Operations()
		methods := make([]string, 0, len(ops))
		for m := range ops {
			methods = append(methods, m)
		}
		sort.Strings(methods)
		for _, m := range methods {
			op := ops[m]
			auth := globalAuth
			if op.Security != nil {
				auth = len(*op.Security) > 0
			}
			e := Endpoint{Method: strings.ToUpper(m), Path: p, Summary: op.Summary, Auth: auth, Source: "openapi"}
			u.addEndpoint(e)
			key := e.Key()

			fmt.Fprintf(&b, "### %s", key)
			if op.Summary != "" {
				fmt.Fprintf(&b, " — %s", op.Summary)
			}
			b.WriteString("\n")
			if auth {
				b.WriteString("Requires authentication.\n")
			}
			if d := strings.TrimSpace(op.Description); d != "" {
				b.WriteString(Truncate(red.Secrets(d), 700))
				b.WriteString("\n")
			}
			params := append(openapi3.Parameters{}, item.Parameters...)
			params = append(params, op.Parameters...)
			for _, pr := range params {
				if pr.Value == nil {
					continue
				}
				v := pr.Value
				fmt.Fprintf(&b, "- %s parameter `%s`", v.In, v.Name)
				if v.Required {
					b.WriteString(" (required)")
				}
				if v.Example != nil {
					fmt.Fprintf(&b, " e.g. %v", v.Example)
				}
				b.WriteString("\n")
				if v.In == "path" {
					consumers = append(consumers, consumer{op: key, resource: resourceForParam(p, v.Name), via: "path {" + v.Name + "}", kind: "id"})
				}
			}
			if rb := op.RequestBody; rb != nil && rb.Value != nil {
				for ct, mt := range rb.Value.Content {
					fmt.Fprintf(&b, "- request body (%s)", ct)
					if ex := mediaExample(mt); ex != "" {
						fmt.Fprintf(&b, ": %s", Truncate(red.Secrets(ex), sampleBodyBytes))
					}
					b.WriteString("\n")
					if mt.Schema != nil && mt.Schema.Value != nil {
						for name := range mt.Schema.Value.Properties {
							if r := idResource(name); r != "" {
								consumers = append(consumers, consumer{op: key, resource: r, via: "json " + name, kind: "id"})
							}
						}
					}
					break
				}
			}
			if auth {
				consumers = append(consumers, consumer{op: key, via: "Authorization header", kind: "token"})
			}
			if op.Responses != nil {
				codes := make([]string, 0)
				for code := range op.Responses.Map() {
					codes = append(codes, code)
				}
				sort.Strings(codes)
				for _, code := range codes {
					r := op.Responses.Value(code)
					if r == nil || r.Value == nil {
						continue
					}
					desc := ""
					if r.Value.Description != nil {
						desc = strings.TrimSpace(*r.Value.Description)
					}
					fmt.Fprintf(&b, "- response %s %s", code, Truncate(desc, 160))
					if strings.HasPrefix(code, "2") {
						for name := range r.Value.Headers {
							if strings.EqualFold(name, "Set-Cookie") {
								producers = append(producers, producer{op: key, path: "cookie", kind: "cookie"})
							}
						}
						for _, mt := range r.Value.Content {
							if ex := mediaExample(mt); ex != "" {
								fmt.Fprintf(&b, ": %s", Truncate(red.Secrets(ex), sampleBodyBytes))
							}
							if mt.Schema != nil {
								producers = append(producers, schemaProducers(key, p, "$", mt.Schema, 0)...)
							}
							break
						}
					}
					b.WriteString("\n")
				}
			}
			b.WriteString("\n")
		}
	}

	// Link producers to consumers, keeping the two most natural sources of
	// each value: a listing (GET without parameters) or a create (POST)
	// rather than a delete or a detail page.
	for _, c := range consumers {
		var cands []Dependency
		var scores []int
		for _, pr := range producers {
			if pr.op == c.op || strings.HasPrefix(pr.op, "DELETE ") {
				continue
			}
			var d Dependency
			switch {
			case c.kind == "token" && pr.kind == "token":
				d = Dependency{Producer: pr.op, Value: pr.path, Consumer: c.op, Via: "Authorization: Bearer", Kind: "token"}
			case c.kind == "token" && pr.kind == "cookie":
				d = Dependency{Producer: pr.op, Value: "Set-Cookie", Consumer: c.op, Via: "session cookie (kept automatically)", Kind: "cookie"}
			case c.kind == "id" && pr.kind == "id" && c.resource != "" && pr.resource == c.resource:
				d = Dependency{Producer: pr.op, Value: pr.path, Consumer: c.op, Via: c.via, Kind: "id"}
			default:
				continue
			}
			score := 0
			if strings.HasPrefix(pr.op, "GET ") && !strings.Contains(pr.op, "{") {
				score += 2
			}
			if strings.HasPrefix(pr.op, "POST ") {
				score++
			}
			if pr.field == "id" {
				score++
			}
			cands = append(cands, d)
			scores = append(scores, score)
		}
		idx := make([]int, len(cands))
		for i := range idx {
			idx[i] = i
		}
		sort.SliceStable(idx, func(a, b int) bool { return scores[idx[a]] > scores[idx[b]] })
		for i := 0; i < len(idx) && i < 2; i++ {
			u.addDependency(cands[idx[i]])
		}
	}
	return Truncate(b.String(), maxSpecDigest) + "\n", nil
}

func mediaExample(mt *openapi3.MediaType) string {
	if mt == nil {
		return ""
	}
	var v any
	switch {
	case mt.Example != nil:
		v = mt.Example
	case len(mt.Examples) > 0:
		names := make([]string, 0, len(mt.Examples))
		for n := range mt.Examples {
			names = append(names, n)
		}
		sort.Strings(names)
		if ex := mt.Examples[names[0]]; ex != nil && ex.Value != nil {
			v = ex.Value.Value
		}
	case mt.Schema != nil && mt.Schema.Value != nil && mt.Schema.Value.Example != nil:
		v = mt.Schema.Value.Example
	}
	if v == nil {
		return ""
	}
	out, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(out)
}

var tokenFieldRe = regexp.MustCompile(`(?i)^(access_?token|token|id_?token|jwt|session_?token|auth_?token|bearer)$`)

// schemaProducers lists token and id fields in a response schema.
func schemaProducers(op, opPath, jp string, ref *openapi3.SchemaRef, depth int) []producer {
	if ref == nil || ref.Value == nil || depth > 3 {
		return nil
	}
	s := ref.Value
	var out []producer
	if s.Items != nil {
		return schemaProducers(op, opPath, jp+"[0]", s.Items, depth+1)
	}
	names := make([]string, 0, len(s.Properties))
	for n := range s.Properties {
		names = append(names, n)
	}
	sort.Strings(names)
	parent := lastResource(opPath)
	if i := strings.LastIndexByte(jp, '.'); i > 0 {
		parent = singular(strings.TrimSuffix(jp[i+1:], "[0]"))
	}
	for _, n := range names {
		p := jp + "." + n
		switch {
		case tokenFieldRe.MatchString(n):
			out = append(out, producer{op: op, path: p, field: n, kind: "token"})
		case n == "id":
			if parent != "" {
				out = append(out, producer{op: op, path: p, field: n, resource: parent, kind: "id"})
			}
		case idResource(n) != "":
			out = append(out, producer{op: op, path: p, field: n, resource: idResource(n), kind: "id"})
		}
		if c := s.Properties[n]; c != nil && c.Value != nil && (c.Value.Items != nil || len(c.Value.Properties) > 0) {
			out = append(out, schemaProducers(op, opPath, p, c, depth+1)...)
		}
	}
	return out
}

var idSuffixRe = regexp.MustCompile(`^([a-z][A-Za-z0-9]*?)(Id|_id|ID)$`)

// idResource maps "productId" or "order_id" to "product" or "order".
func idResource(field string) string {
	m := idSuffixRe.FindStringSubmatch(field)
	if m == nil {
		return ""
	}
	return strings.ToLower(m[1])
}

// resourceForParam names the resource a path parameter identifies:
// /api/products/{id} -> product; /api/cart/{productId} -> product.
func resourceForParam(path, param string) string {
	if r := idResource(param); r != "" {
		return r
	}
	segs := strings.Split(strings.Trim(path, "/"), "/")
	for i, s := range segs {
		if s == "{"+param+"}" && i > 0 && !strings.HasPrefix(segs[i-1], "{") {
			return singular(segs[i-1])
		}
	}
	return ""
}

// lastResource is the last literal path segment, singular.
func lastResource(path string) string {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	for i := len(segs) - 1; i >= 0; i-- {
		if s := segs[i]; s != "" && !strings.HasPrefix(s, "{") {
			return singular(s)
		}
	}
	return ""
}

func singular(s string) string {
	s = strings.ToLower(s)
	switch {
	case strings.HasSuffix(s, "ies"):
		return s[:len(s)-3] + "y"
	case strings.HasSuffix(s, "ses"):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss"):
		return s[:len(s)-1]
	}
	return s
}

// Paths observed in traffic.

var (
	numSegRe  = regexp.MustCompile(`^\d+$`)
	uuidSegRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	hexSegRe  = regexp.MustCompile(`^[0-9a-fA-F]{16,}$`)
)

// NormalizePath turns a concrete path into a template: numeric, UUID and
// long hex segments become {id}.
func NormalizePath(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if numSegRe.MatchString(s) || uuidSegRe.MatchString(s) || hexSegRe.MatchString(s) {
			segs[i] = "{id}"
		}
	}
	out := strings.Join(segs, "/")
	if out == "" {
		out = "/"
	}
	return out
}

var staticExtRe = regexp.MustCompile(`(?i)\.(js|mjs|css|map|png|jpe?g|gif|svg|ico|webp|avif|woff2?|ttf|eot|otf|mp4|webm)$`)

// HAR.

type harFile struct {
	Log struct {
		Entries []harEntry `json:"entries"`
	} `json:"log"`
}

type harNV struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type harEntry struct {
	StartedDateTime string `json:"startedDateTime"`
	Request         struct {
		Method   string  `json:"method"`
		URL      string  `json:"url"`
		Headers  []harNV `json:"headers"`
		PostData *struct {
			MimeType string `json:"mimeType"`
			Text     string `json:"text"`
		} `json:"postData"`
	} `json:"request"`
	Response struct {
		Status  int     `json:"status"`
		Headers []harNV `json:"headers"`
		Content struct {
			MimeType string `json:"mimeType"`
			Text     string `json:"text"`
			Encoding string `json:"encoding"`
		} `json:"content"`
	} `json:"response"`
}

// producedValue is a value seen in a response, kept only in memory to find
// where later requests reuse it. It is never sent to a model.
type producedValue struct {
	value string
	op    string
	from  string // JSONPath, "cookie name" or "header Name"
}

func (u *Understanding) fromHAR(src []byte, red *Redactor) (string, error) {
	var h harFile
	if err := json.Unmarshal(src, &h); err != nil {
		return "", fmt.Errorf("read HAR file: %w", err)
	}
	var entries []harEntry
	for _, e := range h.Log.Entries {
		pu, err := url.Parse(e.Request.URL)
		if err != nil || pu.Host == "" {
			continue
		}
		if staticExtRe.MatchString(pu.Path) || strings.HasPrefix(e.Response.Content.MimeType, "image/") || strings.HasPrefix(e.Response.Content.MimeType, "font/") {
			continue
		}
		if ThirdPartyCategory(pu.Hostname()) != "" {
			continue
		}
		entries = append(entries, e)
	}
	if len(entries) == 0 {
		return "", errors.New("the HAR file has no API requests (static assets and third-party calls are ignored)")
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].StartedDateTime < entries[j].StartedDateTime })
	u.HasSpec = true

	var produced []producedValue
	var b strings.Builder
	b.WriteString("## Recorded traffic (HAR, in order, redacted)\n\n")
	for i, e := range entries {
		pu, _ := url.Parse(e.Request.URL)
		path := NormalizePath(pu.Path)
		key := strings.ToUpper(e.Request.Method) + " " + path
		auth := false
		for _, hd := range e.Request.Headers {
			if strings.EqualFold(hd.Name, "Authorization") || strings.EqualFold(hd.Name, "Cookie") {
				auth = true
			}
		}
		u.addEndpoint(Endpoint{Method: strings.ToUpper(e.Request.Method), Path: path, Auth: auth, Source: "har", Count: 1})

		// Which earlier values does this request reuse?
		reqText := e.Request.URL
		for _, hd := range e.Request.Headers {
			reqText += "\n" + hd.Name + ": " + hd.Value
		}
		if e.Request.PostData != nil {
			reqText += "\n" + e.Request.PostData.Text
		}
		for _, pv := range produced {
			if pv.op == key || !strings.Contains(reqText, pv.value) {
				continue
			}
			u.addDependency(Dependency{Producer: pv.op, Value: pv.from, Consumer: key, Via: whereUsed(e, pv.value), Kind: kindOf(pv.from)})
		}

		if i < maxHAREntries {
			fmt.Fprintf(&b, "%d. %s %s → %d\n", i+1, strings.ToUpper(e.Request.Method), red.URL(pu.RequestURI()), e.Response.Status)
			if e.Request.PostData != nil && e.Request.PostData.Text != "" {
				fmt.Fprintf(&b, "   request: %s\n", red.Body([]byte(e.Request.PostData.Text), sampleBodyBytes))
			}
			if body := harBody(e); len(body) > 0 && strings.Contains(e.Response.Content.MimeType, "json") {
				fmt.Fprintf(&b, "   response: %s\n", red.Body(body, sampleBodyBytes))
			}
		}

		// Remember values this response produces.
		for _, hd := range e.Response.Headers {
			if strings.EqualFold(hd.Name, "Set-Cookie") {
				name, val, _ := strings.Cut(strings.SplitN(hd.Value, ";", 2)[0], "=")
				if len(val) >= 6 {
					produced = append(produced, producedValue{value: val, op: key, from: "cookie " + strings.TrimSpace(name)})
				}
			}
			if strings.EqualFold(hd.Name, "Location") && hd.Value != "" {
				produced = append(produced, producedValue{value: hd.Value, op: key, from: "header Location"})
			}
		}
		if body := harBody(e); len(body) > 0 && gjson.ValidBytes(body) {
			collectValues(gjson.ParseBytes(body), "$", key, &produced, 0)
		}
	}
	if len(entries) > maxHAREntries {
		fmt.Fprintf(&b, "… %d more requests not shown\n", len(entries)-maxHAREntries)
	}
	return Truncate(b.String(), maxHARDigest) + "\n", nil
}

func harBody(e harEntry) []byte {
	c := e.Response.Content
	if c.Encoding == "base64" {
		b, err := base64.StdEncoding.DecodeString(c.Text)
		if err != nil {
			return nil
		}
		return b
	}
	return []byte(c.Text)
}

// collectValues records string values long enough to be recognisable and
// integer ids of three or more digits.
func collectValues(r gjson.Result, path, op string, out *[]producedValue, depth int) {
	if depth > 4 || len(*out) > 5000 {
		return
	}
	switch {
	case r.IsObject():
		r.ForEach(func(k, v gjson.Result) bool {
			collectValues(v, path+"."+k.String(), op, out, depth+1)
			return true
		})
	case r.IsArray():
		for i, v := range r.Array() {
			if i >= 3 {
				break
			}
			collectValues(v, path+"["+strconv.Itoa(i)+"]", op, out, depth+1)
		}
	case r.Type == gjson.String:
		if len(r.Str) >= 6 && !strings.ContainsAny(r.Str, " \n") {
			*out = append(*out, producedValue{value: r.Str, op: op, from: path})
		}
	case r.Type == gjson.Number:
		if len(r.Raw) >= 3 && !strings.ContainsAny(r.Raw, ".eE-") {
			*out = append(*out, producedValue{value: r.Raw, op: op, from: path})
		}
	}
}

func whereUsed(e harEntry, v string) string {
	for _, hd := range e.Request.Headers {
		if strings.Contains(hd.Value, v) {
			if strings.EqualFold(hd.Name, "Authorization") {
				return "Authorization header"
			}
			if strings.EqualFold(hd.Name, "Cookie") {
				return "cookie (kept automatically)"
			}
			return "header " + hd.Name
		}
	}
	if pu, err := url.Parse(e.Request.URL); err == nil {
		if strings.Contains(pu.Path, v) {
			return "path"
		}
		if strings.Contains(pu.RawQuery, v) {
			return "query"
		}
	}
	return "request body"
}

func kindOf(from string) string {
	switch {
	case strings.HasPrefix(from, "cookie"):
		return "cookie"
	case tokenFieldRe.MatchString(from[strings.LastIndexAny(from, ".[")+1:]):
		return "token"
	}
	return "id"
}

// Access logs.

var (
	clfRe     = regexp.MustCompile(`^(\S+) \S+ \S+ \[([^\]]+)\] "([A-Z]+) (\S+)[^"]*" (\d{3})`)
	looseReqs = regexp.MustCompile(`\b(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS) (/[^\s"?]*)(?:\?[^\s"]*)?(?:[^"\n]*")?\s*(\d{3})?`)
)

type logHit struct {
	client string
	at     time.Time
	ep     string
	status int
}

// fromLog estimates the traffic mix: each endpoint's share of requests and
// the most common visits (requests from one client with gaps under 30
// minutes). Client addresses and query strings never leave this function.
func (u *Understanding) fromLog(src []byte) (string, error) {
	var hits []logHit
	lines := strings.Split(string(src), "\n")
	if len(lines) > maxLogLines {
		lines = lines[:maxLogLines]
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var h logHit
		if m := clfRe.FindStringSubmatch(line); m != nil {
			h.client = m[1]
			h.at, _ = time.Parse("02/Jan/2006:15:04:05 -0700", m[2])
			pu, err := url.Parse(m[4])
			if err != nil {
				continue
			}
			h.ep = m[3] + " " + NormalizePath(pu.Path)
			h.status, _ = strconv.Atoi(m[5])
			if staticExtRe.MatchString(pu.Path) {
				continue
			}
		} else if m := looseReqs.FindStringSubmatch(line); m != nil {
			if staticExtRe.MatchString(m[2]) {
				continue
			}
			h.ep = m[1] + " " + NormalizePath(m[2])
			h.status, _ = strconv.Atoi(m[3])
		} else {
			continue
		}
		hits = append(hits, h)
	}
	if len(hits) == 0 {
		return "", errors.New("no requests found in the access log (expected common/combined log format or lines with \"METHOD /path\")")
	}

	counts := map[string]int{}
	errs := map[string]int{}
	for _, h := range hits {
		counts[h.ep]++
		if h.status >= 400 {
			errs[h.ep]++
		}
	}
	for ep, n := range counts {
		u.Mix = append(u.Mix, MixEntry{Endpoint: ep, Count: n, Share: float64(n) / float64(len(hits))})
		method, path, _ := strings.Cut(ep, " ")
		u.addEndpoint(Endpoint{Method: method, Path: path, Source: "log", Count: n})
	}
	sort.Slice(u.Mix, func(i, j int) bool {
		if u.Mix[i].Count != u.Mix[j].Count {
			return u.Mix[i].Count > u.Mix[j].Count
		}
		return u.Mix[i].Endpoint < u.Mix[j].Endpoint
	})

	// Visits per client.
	byClient := map[string][]logHit{}
	for _, h := range hits {
		if h.client != "" {
			byClient[h.client] = append(byClient[h.client], h)
		}
	}
	patterns := map[string]int{}
	visits := 0
	for _, hs := range byClient {
		sort.SliceStable(hs, func(i, j int) bool { return hs[i].at.Before(hs[j].at) })
		var cur []string
		flush := func() {
			if len(cur) > 0 {
				patterns[strings.Join(cur, " → ")]++
				visits++
			}
			cur = nil
		}
		for i, h := range hs {
			if i > 0 && !h.at.IsZero() && h.at.Sub(hs[i-1].at) > 30*time.Minute {
				flush()
			}
			if len(cur) == 0 || cur[len(cur)-1] != h.ep {
				if len(cur) < 12 {
					cur = append(cur, h.ep)
				}
			}
		}
		flush()
	}
	for p, n := range patterns {
		u.Visits = append(u.Visits, VisitPattern{Steps: strings.Split(p, " → "), Count: n, Share: float64(n) / float64(visits)})
	}
	sort.Slice(u.Visits, func(i, j int) bool {
		if u.Visits[i].Count != u.Visits[j].Count {
			return u.Visits[i].Count > u.Visits[j].Count
		}
		return strings.Join(u.Visits[i].Steps, ",") < strings.Join(u.Visits[j].Steps, ",")
	})
	if len(u.Visits) > maxVisitPatterns {
		u.Visits = u.Visits[:maxVisitPatterns]
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## Traffic mix (from an access log of %d requests)\n\nUse this to choose journey weights.\n\n", len(hits))
	for i, m := range u.Mix {
		if i >= 40 {
			fmt.Fprintf(&b, "- … %d more endpoints\n", len(u.Mix)-40)
			break
		}
		fmt.Fprintf(&b, "- %s: %.1f%% (%d requests", m.Endpoint, m.Share*100, m.Count)
		if e := errs[m.Endpoint]; e > 0 {
			fmt.Fprintf(&b, ", %.0f%% errors", float64(e)*100/float64(m.Count))
		}
		b.WriteString(")\n")
	}
	if len(u.Visits) > 0 {
		fmt.Fprintf(&b, "\nMost common visits (%d visits from %d clients):\n", visits, len(byClient))
		for _, v := range u.Visits {
			fmt.Fprintf(&b, "- %.1f%%: %s\n", v.Share*100, strings.Join(v.Steps, " → "))
		}
	}
	b.WriteString("\n")
	return b.String(), nil
}
