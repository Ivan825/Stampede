// Package scenario defines the Stampede scenario file format: its types,
// parsing, validation and the templating used inside steps.
package scenario

// APIVersion is the scenario format version this build reads and writes.
const APIVersion = "stampede.dev/v1"

// KindScenario is the only document kind currently defined.
const KindScenario = "Scenario"

// Scenario is a complete load test definition: what to hit, how users
// behave, how much load to apply and what counts as passing.
type Scenario struct {
	APIVersion string            `yaml:"apiVersion" json:"apiVersion"`
	Kind       string            `yaml:"kind" json:"kind"`
	Metadata   Metadata          `yaml:"metadata" json:"metadata"`
	Target     Target            `yaml:"target" json:"target"`
	Vars       map[string]any    `yaml:"vars,omitempty" json:"vars,omitempty"`
	Data       map[string]Feeder `yaml:"data,omitempty" json:"data,omitempty"`
	Journeys   []Journey         `yaml:"journeys" json:"journeys"`
	Load       Load              `yaml:"load" json:"load"`
	Targets    []string          `yaml:"targets,omitempty" json:"targets,omitempty"`
}

// Metadata names and labels a scenario.
type Metadata struct {
	Name        string   `yaml:"name" json:"name"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// Target describes the system under test and how to connect to it.
type Target struct {
	// BaseURL is prefixed to relative request paths. It may be templated,
	// for example ${env.TARGET_URL}.
	BaseURL string `yaml:"baseURL" json:"baseURL"`
	// Verify names the ownership check used for public targets:
	// "dns-txt" or "well-known". Private and loopback hosts need none.
	Verify  string            `yaml:"verify,omitempty" json:"verify,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	// Timeout is the default per-request timeout (default 30s).
	Timeout Duration    `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	HTTP    HTTPOptions `yaml:"http,omitempty" json:"http,omitempty"`
}

// HTTPOptions tunes connection behaviour so it resembles real clients.
type HTTPOptions struct {
	// Connections is "per-vu" (each virtual user keeps its own keep-alive
	// connections, like browsers; the default) or "shared" (one pool for
	// all users, like a service client).
	Connections string `yaml:"connections,omitempty" json:"connections,omitempty"`
	// HTTP2 enables HTTP/2 over TLS when the server offers it.
	HTTP2 bool `yaml:"http2,omitempty" json:"http2,omitempty"`
	// H2C speaks HTTP/2 without TLS to http:// targets, with prior
	// knowledge rather than an upgrade from HTTP/1.1.
	H2C bool `yaml:"h2c,omitempty" json:"h2c,omitempty"`
	// DisableKeepAlive opens a new connection for every request.
	DisableKeepAlive bool `yaml:"disableKeepAlive,omitempty" json:"disableKeepAlive,omitempty"`
	// InsecureSkipVerify disables TLS certificate checks (test targets only).
	InsecureSkipVerify bool `yaml:"insecureSkipVerify,omitempty" json:"insecureSkipVerify,omitempty"`
	// DNSCacheTTL is how long a host name lookup is reused across
	// connections (default 30s). Set "0s" to resolve on every connection.
	DNSCacheTTL *Duration `yaml:"dnsCacheTTL,omitempty" json:"dnsCacheTTL,omitempty"`
	// MaxRedirects caps followed redirects (default 10, 0 keeps the default,
	// -1 disables following).
	MaxRedirects int `yaml:"maxRedirects,omitempty" json:"maxRedirects,omitempty"`
}

// Feeder supplies test data rows to virtual users.
type Feeder struct {
	CSV  string `yaml:"csv,omitempty" json:"csv,omitempty"`
	JSON string `yaml:"json,omitempty" json:"json,omitempty"`
	List []any  `yaml:"list,omitempty" json:"list,omitempty"`
	// Range generates integers from Range[0] to Range[1] inclusive.
	Range []int64 `yaml:"range,omitempty" json:"range,omitempty"`
	// Mode is one of unique, sequential, random or per-vu (default sequential).
	Mode string `yaml:"mode,omitempty" json:"mode,omitempty"`
	// OnExhausted applies to unique mode: "stop" ends the virtual user,
	// "wrap" starts again from the first row (default stop).
	OnExhausted string `yaml:"onExhausted,omitempty" json:"onExhausted,omitempty"`
}

// Feeder modes.
const (
	FeedSequential = "sequential"
	FeedUnique     = "unique"
	FeedRandom     = "random"
	FeedPerVU      = "per-vu"
)

// Journey is one path a user takes through the product.
type Journey struct {
	Name   string       `yaml:"name" json:"name"`
	Weight int          `yaml:"weight,omitempty" json:"weight,omitempty"`
	Tags   []string     `yaml:"tags,omitempty" json:"tags,omitempty"`
	Target *JourneyGoal `yaml:"target,omitempty" json:"target,omitempty"`
	Steps  []Step       `yaml:"steps" json:"steps"`
}

// JourneyGoal is a per-journey target, stricter or looser than the global one.
type JourneyGoal struct {
	P50    Duration `yaml:"p50,omitempty" json:"p50,omitempty"`
	P90    Duration `yaml:"p90,omitempty" json:"p90,omitempty"`
	P95    Duration `yaml:"p95,omitempty" json:"p95,omitempty"`
	P99    Duration `yaml:"p99,omitempty" json:"p99,omitempty"`
	Errors *Percent `yaml:"errors,omitempty" json:"errors,omitempty"`
}

// StepKind identifies what a step does.
type StepKind string

// Step kinds.
const (
	StepRequest StepKind = "request"
	StepThink   StepKind = "think"
	StepBranch  StepKind = "branch"
	StepLoop    StepKind = "loop"
	StepWhile   StepKind = "while"
	StepGroup   StepKind = "group"
	StepScript  StepKind = "script"
	StepGraphQL StepKind = "graphql"
	StepSSE     StepKind = "sse"
	StepWS      StepKind = "ws"
	StepSend    StepKind = "send"
	StepExpect  StepKind = "expect"
	StepGRPC    StepKind = "grpc"
)

// Step is one action within a journey. Exactly one kind-specific field is
// set; Kind records which.
type Step struct {
	Kind StepKind
	// Name labels the step in reports. Requests default to "METHOD path".
	Name string
	// If skips the step unless the expression is true.
	If string

	Request *Request
	Think   *ThinkTime
	Branch  []Branch
	// Loop runs Steps Count times; While runs them while Cond holds, at
	// most Max times.
	Loop    *Loop
	Group   *Group
	Script  string
	GraphQL *GraphQL
	SSE     *SSE
	WS      *WebSocket
	Send    *Send
	Expect  *Expect
	GRPC    *GRPC
}

// Request is an HTTP call.
type Request struct {
	Method  string
	URL     string
	Headers map[string]string
	Query   map[string]string
	// Exactly one body form may be set.
	JSON any
	Body string
	Form map[string]string

	Check   *Check
	Extract map[string]string
	Timeout Duration
}

// GraphQL is a GraphQL operation sent as an HTTP POST. Request holds the
// endpoint, headers, check, extract and timeout.
type GraphQL struct {
	Request
	Query         string
	Variables     any
	OperationName string
	// Persisted sends an automatic persisted query (APQ): the query's
	// SHA-256 first, and the full query only if the server does not know it.
	Persisted *Persisted
}

// Persisted configures an automatic persisted query. An empty SHA256 is
// computed from the query.
type Persisted struct {
	SHA256 string `yaml:"sha256,omitempty" json:"sha256,omitempty"`
}

// SSE reads a server-sent event stream. Request holds the HTTP parts; the
// method defaults to GET, or POST when a body is set (as LLM APIs expect).
type SSE struct {
	Request
	Until SSEUntil
}

// SSEUntil says when to stop reading a stream. Events and Match are
// requirements: the stream must deliver them. Duration caps the stream;
// on its own, reaching it ends the step successfully. With none set the
// step reads until the server closes the stream.
type SSEUntil struct {
	Events   int      `yaml:"events,omitempty" json:"events,omitempty"`
	Match    string   `yaml:"match,omitempty" json:"match,omitempty"`
	Duration Duration `yaml:"duration,omitempty" json:"duration,omitempty"`
}

// WebSocket opens a connection, runs Steps with it and closes it when they
// finish (or the iteration fails). URL may be ws://, wss://, http(s):// or
// a path joined to the base URL. Inside Steps, send and expect steps use
// the connection; any other step kind may be mixed in.
type WebSocket struct {
	URL          string
	Headers      map[string]string
	Subprotocols []string
	// Timeout bounds the opening handshake.
	Timeout Duration
	Steps   []Step
}

// Send writes one text message: Text is a template, JSON a JSON template
// sent as text.
type Send struct {
	Text string
	JSON any
}

// Expect waits for a message that matches every condition given; other
// messages are skipped. With no condition the next message matches.
type Expect struct {
	// Match is a regex over the message.
	Match string `yaml:"match,omitempty" json:"match,omitempty"`
	// JSON maps a JSONPath to the expected value, or "exists".
	JSON    map[string]any `yaml:"json,omitempty" json:"json,omitempty"`
	Timeout Duration       `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	// Extract reads variables from the matching message.
	Extract map[string]string `yaml:"-" json:"-"`
}

// GRPC calls a unary or server-streaming gRPC method.
type GRPC struct {
	// Method is "package.Service/Method".
	Method string
	// Target is grpc://host:port or grpcs://host:port; empty means the
	// host of target.baseURL (TLS for https). It may use env, secret and
	// vars, which are rendered once when the run starts.
	Target string
	// Message is the request as JSON (protojson field names); strings may
	// contain ${} expressions.
	Message  any
	Metadata map[string]string
	// Descriptors come from server reflection unless Protoset (a
	// FileDescriptorSet) or Proto (source files, found under ImportPaths)
	// is set.
	Protoset    string
	Proto       []string
	ImportPaths []string
	Check       *GRPCCheck
	Extract     map[string]string
	Timeout     Duration
}

// GRPCCheck asserts on a gRPC response. Status lists the accepted status
// codes by name (default OK); the rest work as for HTTP, on the response
// rendered as JSON.
type GRPCCheck struct {
	Status     GRPCCodes      `yaml:"status,omitempty" json:"status,omitempty"`
	JSON       map[string]any `yaml:"json,omitempty" json:"json,omitempty"`
	MaxLatency Duration       `yaml:"maxLatency,omitempty" json:"maxLatency,omitempty"`
	Expr       string         `yaml:"expr,omitempty" json:"expr,omitempty"`
}

// GRPCCodes is one status code name or a list of them.
type GRPCCodes []string

// Branch is one weighted alternative inside a branch step.
type Branch struct {
	Weight int    `yaml:"weight" json:"weight"`
	Name   string `yaml:"name,omitempty" json:"name,omitempty"`
	Steps  []Step `yaml:"steps" json:"steps"`
}

// Loop repeats steps.
type Loop struct {
	Count int
	Cond  string
	Max   int
	Steps []Step
}

// Group names a set of steps so they are reported together.
type Group struct {
	Name  string
	Steps []Step
}

// Check asserts on a response. Failed checks count as errors.
type Check struct {
	// Status accepts exact codes (200), lists ([200, 201]) or classes ("2xx").
	Status       StatusMatcher `yaml:"status,omitempty" json:"status,omitempty"`
	BodyContains string        `yaml:"bodyContains,omitempty" json:"bodyContains,omitempty"`
	// JSON maps a JSONPath to the expected value. The special value
	// "exists" only requires the path to be present.
	JSON       map[string]any `yaml:"json,omitempty" json:"json,omitempty"`
	MaxLatency Duration       `yaml:"maxLatency,omitempty" json:"maxLatency,omitempty"`
	// Expr is a boolean expression over status, headers, body and json.
	Expr string `yaml:"expr,omitempty" json:"expr,omitempty"`
	// AllowErrors (GraphQL only) accepts a response whose errors array is
	// not empty; by default that fails the step.
	AllowErrors bool `yaml:"allowErrors,omitempty" json:"allowErrors,omitempty"`
}

// Load describes how much traffic to generate and its shape over time.
type Load struct {
	// Shape picks a preset (smoke, baseline, stress, spike, soak,
	// breakpoint, steps, recovery, wave). Explicit Stages override it.
	Shape string `yaml:"shape,omitempty" json:"shape,omitempty"`
	// Mode is "rate" (open model: start journeys on a schedule) or "vus"
	// (closed model: a fixed population loops). Default vus.
	Mode string `yaml:"mode,omitempty" json:"mode,omitempty"`

	VUs      int      `yaml:"vus,omitempty" json:"vus,omitempty"`
	Rate     Rate     `yaml:"rate,omitempty" json:"rate,omitempty"`
	Duration Duration `yaml:"duration,omitempty" json:"duration,omitempty"`

	// Start and Max bound preset shapes. In rate mode they are rates
	// ("50/s"); in vus mode they are user counts.
	Start string `yaml:"start,omitempty" json:"start,omitempty"`
	Max   string `yaml:"max,omitempty" json:"max,omitempty"`

	Stages []Stage `yaml:"stages,omitempty" json:"stages,omitempty"`

	// Steps and StepDuration tune the breakpoint and steps shapes; Cycles
	// tunes the wave shape.
	Steps        int      `yaml:"steps,omitempty" json:"steps,omitempty"`
	StepDuration Duration `yaml:"stepDuration,omitempty" json:"stepDuration,omitempty"`
	Cycles       int      `yaml:"cycles,omitempty" json:"cycles,omitempty"`

	// Iterations runs a fixed number of journeys in total, shared by VUs.
	Iterations int `yaml:"iterations,omitempty" json:"iterations,omitempty"`

	// MaxVUs caps the user pool in rate mode (default: sized automatically).
	MaxVUs int `yaml:"maxVUs,omitempty" json:"maxVUs,omitempty"`
	// GracefulStop lets in-flight iterations finish after the end (default 30s).
	GracefulStop Duration `yaml:"gracefulStop,omitempty" json:"gracefulStop,omitempty"`
	// Abort stops the run early when the target is clearly failing.
	Abort *Abort `yaml:"abort,omitempty" json:"abort,omitempty"`
}

// Abort ends a run when errors or latency stay above a limit for a while,
// so a broken target is not hammered for the rest of the test.
type Abort struct {
	// Errors is the failed-request ratio that trips the abort.
	Errors *Percent `yaml:"errors,omitempty" json:"errors,omitempty"`
	// P95 is the latency that trips the abort.
	P95 Duration `yaml:"p95,omitempty" json:"p95,omitempty"`
	// For is how long the limit must be exceeded (default 10s).
	For Duration `yaml:"for,omitempty" json:"for,omitempty"`
}

// Load modes.
const (
	ModeVUs  = "vus"
	ModeRate = "rate"
)

// Stage linearly ramps to Target over Duration. Target is a user count in
// vus mode or a rate ("200/s") in rate mode.
type Stage struct {
	Duration Duration `yaml:"duration" json:"duration"`
	Target   string   `yaml:"target" json:"target"`
}
