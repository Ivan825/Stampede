package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/Ivan825/Stampede/internal/protocol/grpcx"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// GRPCMethod is a callable method of a gRPC service from .proto files.
type GRPCMethod struct {
	// Name is "package.Service/Method", as grpc steps write it.
	Name            string `json:"name"`
	Input           string `json:"input"`
	Output          string `json:"output"`
	ServerStreaming bool   `json:"serverStreaming,omitempty"`
	// Example is the request message as JSON with every field set to an
	// example value of its type.
	Example string `json:"example"`
	Comment string `json:"comment,omitempty"`
}

// Limits on proto input.
const (
	maxProtoMethods = 80
	maxProtoDigest  = 40_000
)

// fromProto compiles .proto sources and lists their services' methods for
// the model. Client and bidirectional streaming methods are left out, as
// grpc steps call only unary and server streaming methods.
func (u *Understanding) fromProto(sources map[string][]byte, paths []string) (string, error) {
	files, err := grpcx.CompileSources(context.Background(), sources)
	if err != nil {
		return "", fmt.Errorf("proto: %w", err)
	}
	u.ProtoFiles = files
	names := make([]string, 0, len(sources))
	for n := range sources {
		names = append(names, n)
	}
	sort.Strings(names)
	var skipped []string
	for _, name := range names {
		fd, err := files.FindFileByPath(name)
		if err != nil {
			continue
		}
		svcs := fd.Services()
		for i := 0; i < svcs.Len(); i++ {
			sd := svcs.Get(i)
			ms := sd.Methods()
			for j := 0; j < ms.Len(); j++ {
				m := ms.Get(j)
				full := string(sd.FullName()) + "/" + string(m.Name())
				if m.IsStreamingClient() {
					skipped = append(skipped, full)
					continue
				}
				gm := GRPCMethod{
					Name: full, Input: string(m.Input().FullName()), Output: string(m.Output().FullName()),
					ServerStreaming: m.IsStreamingServer(), Example: exampleJSON(m.Input()),
					Comment: strings.TrimSpace(fd.SourceLocations().ByDescriptor(m).LeadingComments),
				}
				u.GRPCMethods = append(u.GRPCMethods, gm)
				u.addEndpoint(Endpoint{Method: "GRPC", Path: full, Summary: firstLine(gm.Comment), Source: "proto"})
			}
		}
	}
	if len(u.GRPCMethods) == 0 {
		return "", errors.New("the .proto files define no unary or server streaming methods")
	}
	if len(u.GRPCMethods) > maxProtoMethods {
		u.GRPCMethods = u.GRPCMethods[:maxProtoMethods]
	}

	var b strings.Builder
	b.WriteString("## gRPC services (from .proto files)\n\n")
	b.WriteString("Call these with `grpc:` steps: `grpc: package.Service/Method`, `message:` (the request as JSON with protojson field names), ")
	b.WriteString("`metadata:` for headers such as authorization, and `check: {status: OK}` (or the status codes a call may return). ")
	b.WriteString("Extract values from responses with `extract:` and JSONPath, as for HTTP. Leave `target:` out so calls go to the target's host.")
	if len(paths) > 0 {
		fmt.Fprintf(&b, " Give every grpc step `proto: [%s]` so runs load these descriptors.", strings.Join(paths, ", "))
	} else {
		b.WriteString(" Leave `proto:` out: descriptors come from the server's reflection service.")
	}
	b.WriteString("\n\n")
	for _, m := range u.GRPCMethods {
		kind := "unary"
		if m.ServerStreaming {
			kind = "server streaming"
		}
		fmt.Fprintf(&b, "- %s (%s): %s → %s\n", m.Name, kind, m.Input, m.Output)
		if m.Comment != "" {
			fmt.Fprintf(&b, "  %s\n", Truncate(strings.ReplaceAll(m.Comment, "\n", " "), 200))
		}
		fmt.Fprintf(&b, "  request: %s\n", m.Example)
	}
	if len(skipped) > 0 {
		fmt.Fprintf(&b, "\nNot callable from a scenario (client or bidirectional streaming): %s\n", strings.Join(skipped, ", "))
	}
	b.WriteString("\n")
	return Truncate(b.String(), maxProtoDigest), nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// exampleJSON renders a message with an example value in every field,
// nested messages to a small depth.
func exampleJSON(md protoreflect.MessageDescriptor) string {
	b, err := json.Marshal(exampleMessage(md, 0))
	if err != nil {
		return "{}"
	}
	return string(b)
}

func exampleMessage(md protoreflect.MessageDescriptor, depth int) map[string]any {
	out := map[string]any{}
	if depth > 3 {
		return out
	}
	fs := md.Fields()
	for i := 0; i < fs.Len(); i++ {
		f := fs.Get(i)
		var v any
		switch {
		case f.IsMap():
			v = map[string]any{"key": exampleValue(f.MapValue(), depth)}
		case f.IsList():
			v = []any{exampleValue(f, depth)}
		default:
			v = exampleValue(f, depth)
		}
		out[f.JSONName()] = v
	}
	return out
}

func exampleValue(f protoreflect.FieldDescriptor, depth int) any {
	switch f.Kind() {
	case protoreflect.BoolKind:
		return true
	case protoreflect.StringKind:
		return "string"
	case protoreflect.BytesKind:
		return "Ynl0ZXM="
	case protoreflect.EnumKind:
		vs := f.Enum().Values()
		if vs.Len() > 1 {
			return string(vs.Get(1).Name())
		}
		if vs.Len() == 1 {
			return string(vs.Get(0).Name())
		}
		return 0
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return 1.5
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return "1" // protojson writes 64-bit integers as strings
	case protoreflect.MessageKind, protoreflect.GroupKind:
		switch f.Message().FullName() {
		case "google.protobuf.Timestamp":
			return "2026-01-01T00:00:00Z"
		case "google.protobuf.Duration":
			return "1s"
		}
		return exampleMessage(f.Message(), depth+1)
	default:
		return 1
	}
}

// grpcProblems checks grpc steps against the methods of the .proto files:
// a call to a method they do not define is flagged for review.
func grpcProblems(s *scenario.Scenario, und *Understanding) []Problem {
	if und == nil || len(und.GRPCMethods) == 0 {
		return nil
	}
	known := map[string]bool{}
	for _, m := range und.GRPCMethods {
		known[m.Name] = true
	}
	var out []Problem
	for _, j := range s.Journeys {
		scenario.WalkSteps(j.Steps, func(st scenario.Step) {
			if st.Kind != scenario.StepGRPC || st.GRPC == nil {
				return
			}
			name := strings.TrimPrefix(st.GRPC.Method, "/")
			if !known[name] {
				out = append(out, Problem{Journey: j.Name, Message: fmt.Sprintf("grpc %s is not a method of the .proto files", st.GRPC.Method)})
			}
		})
	}
	return out
}

// protoFiles returns the descriptors compiled from the generator's .proto
// inputs, if any.
func protoFiles(und *Understanding) *protoregistry.Files {
	if und == nil {
		return nil
	}
	return und.ProtoFiles
}
