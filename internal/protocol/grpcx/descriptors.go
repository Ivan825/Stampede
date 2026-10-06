package grpcx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/bufbuild/protocompile"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	reflectv1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
	reflectv1alpha "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// Source says where a method's descriptor comes from: files when Protoset
// or Proto is set, otherwise the server's reflection service.
type Source struct {
	Protoset    string
	Proto       []string
	ImportPaths []string
}

// Files reports whether descriptors come from files.
func (s Source) Files() bool { return s.Protoset != "" || len(s.Proto) > 0 }

func (s Source) key() string {
	return fmt.Sprintf("%s|%q|%q", s.Protoset, s.Proto, s.ImportPaths)
}

// errorTTL is how long a failed lookup is remembered, so a target without
// reflection is not asked again by every user on every iteration.
const errorTTL = 5 * time.Second

// Descriptors resolves and caches method descriptors for a run.
type Descriptors struct {
	mu      sync.Mutex
	files   map[string]*protoregistry.Files
	methods map[string]methodEntry
}

type methodEntry struct {
	m   protoreflect.MethodDescriptor
	err error
	at  time.Time
}

// NewDescriptors returns an empty cache.
func NewDescriptors() *Descriptors {
	return &Descriptors{files: map[string]*protoregistry.Files{}, methods: map[string]methodEntry{}}
}

// Method finds a method by service full name and method name. File
// sources are loaded once; reflection asks the target the first time a
// service is used. Client and bidirectional streaming methods are
// rejected, as only unary and server streaming calls are supported.
func (d *Descriptors) Method(ctx context.Context, conn grpc.ClientConnInterface, t Target, src Source, service, method string) (protoreflect.MethodDescriptor, error) {
	key := src.key() + "|" + service + "/" + method
	if !src.Files() {
		key = "reflect|" + t.String() + "|" + service + "/" + method
	}
	d.mu.Lock()
	e, ok := d.methods[key]
	d.mu.Unlock()
	if ok && (e.err == nil || time.Since(e.at) < errorTTL) {
		return e.m, e.err
	}

	var files *protoregistry.Files
	var err error
	if src.Files() {
		files, err = d.loadFiles(ctx, src)
	} else {
		files, err = Reflect(ctx, conn, service)
	}
	var m protoreflect.MethodDescriptor
	if err == nil {
		m, err = findMethod(files, service, method)
	}
	d.mu.Lock()
	d.methods[key] = methodEntry{m: m, err: err, at: time.Now()}
	d.mu.Unlock()
	return m, err
}

// ErrUnknownMethod wraps lookups of a service or method the descriptors
// do not define, or of a kind of method that cannot be called.
var ErrUnknownMethod = errors.New("unknown gRPC method")

func findMethod(files *protoregistry.Files, service, method string) (protoreflect.MethodDescriptor, error) {
	desc, err := files.FindDescriptorByName(protoreflect.FullName(service))
	if err != nil {
		return nil, fmt.Errorf("%w: service %s not found", ErrUnknownMethod, service)
	}
	sd, ok := desc.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("%w: %s is not a service", ErrUnknownMethod, service)
	}
	m := sd.Methods().ByName(protoreflect.Name(method))
	if m == nil {
		return nil, fmt.Errorf("%w: service %s has no method %s", ErrUnknownMethod, service, method)
	}
	if m.IsStreamingClient() {
		return nil, fmt.Errorf("%w: %s/%s streams from the client; only unary and server streaming methods are supported", ErrUnknownMethod, service, method)
	}
	return m, nil
}

// LoadFiles loads a source's descriptor files, for validating them before
// a run.
func (d *Descriptors) LoadFiles(ctx context.Context, src Source) error {
	_, err := d.loadFiles(ctx, src)
	return err
}

func (d *Descriptors) loadFiles(ctx context.Context, src Source) (*protoregistry.Files, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if f, ok := d.files[src.key()]; ok {
		return f, nil
	}
	var files *protoregistry.Files
	var err error
	if src.Protoset != "" {
		files, err = loadProtoset(src.Protoset)
	} else {
		files, err = compileProtos(ctx, src.Proto, src.ImportPaths)
	}
	if err != nil {
		return nil, err
	}
	d.files[src.key()] = files
	return files, nil
}

// loadProtoset reads a FileDescriptorSet, as written by
// protoc --descriptor_set_out --include_imports or buf build -o.
func loadProtoset(path string) (*protoregistry.Files, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var set descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(b, &set); err != nil {
		return nil, fmt.Errorf("%s is not a protoset (FileDescriptorSet): %w", path, err)
	}
	return buildFiles(set.GetFile())
}

// compileProtos compiles .proto sources; the well-known types are built in.
func compileProtos(ctx context.Context, protos, importPaths []string) (*protoregistry.Files, error) {
	if len(importPaths) == 0 {
		importPaths = []string{"."}
	}
	c := protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{ImportPaths: importPaths}),
	}
	compiled, err := c.Compile(ctx, protos...)
	if err != nil {
		return nil, err
	}
	files := &protoregistry.Files{}
	var register func(fd protoreflect.FileDescriptor) error
	register = func(fd protoreflect.FileDescriptor) error {
		if _, err := files.FindFileByPath(fd.Path()); err == nil {
			return nil
		}
		imports := fd.Imports()
		for i := 0; i < imports.Len(); i++ {
			if err := register(imports.Get(i).FileDescriptor); err != nil {
				return err
			}
		}
		return files.RegisterFile(fd)
	}
	for _, fd := range compiled {
		if err := register(fd); err != nil {
			return nil, err
		}
	}
	return files, nil
}

// buildFiles links file descriptor protos in dependency order. Imports
// missing from the set may be well-known types linked into this binary.
func buildFiles(fds []*descriptorpb.FileDescriptorProto) (*protoregistry.Files, error) {
	files := &protoregistry.Files{}
	pending := append([]*descriptorpb.FileDescriptorProto(nil), fds...)
	for len(pending) > 0 {
		progress := false
		rest := pending[:0]
		for _, fd := range pending {
			if !depsReady(fd, files) {
				rest = append(rest, fd)
				continue
			}
			f, err := protodesc.NewFile(fd, resolver{files})
			if err != nil {
				return nil, err
			}
			if err := files.RegisterFile(f); err != nil {
				return nil, err
			}
			progress = true
		}
		pending = rest
		if !progress {
			return nil, fmt.Errorf("descriptor %s imports files that are not available", pending[0].GetName())
		}
	}
	return files, nil
}

func depsReady(fd *descriptorpb.FileDescriptorProto, files *protoregistry.Files) bool {
	for _, dep := range fd.GetDependency() {
		if _, err := (resolver{files}).FindFileByPath(dep); err != nil {
			return false
		}
	}
	return true
}

// resolver looks in the linked files first, then in the descriptors
// compiled into this binary (the well-known types).
type resolver struct{ files *protoregistry.Files }

func (r resolver) FindFileByPath(p string) (protoreflect.FileDescriptor, error) {
	if f, err := r.files.FindFileByPath(p); err == nil {
		return f, nil
	}
	return protoregistry.GlobalFiles.FindFileByPath(p)
}

func (r resolver) FindDescriptorByName(n protoreflect.FullName) (protoreflect.Descriptor, error) {
	if d, err := r.files.FindDescriptorByName(n); err == nil {
		return d, nil
	}
	return protoregistry.GlobalFiles.FindDescriptorByName(n)
}

// Reflect fetches the files defining service, and everything they import,
// from the server's reflection service (v1, falling back to v1alpha).
func Reflect(ctx context.Context, conn grpc.ClientConnInterface, service string) (*protoregistry.Files, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	fds, err := reflectFiles(ctx, &v1Stream{client: reflectv1.NewServerReflectionClient(conn)}, service)
	if status.Code(err) == codes.Unimplemented {
		fds, err = reflectFiles(ctx, &v1alphaStream{client: reflectv1alpha.NewServerReflectionClient(conn)}, service)
	}
	if status.Code(err) == codes.NotFound {
		return nil, fmt.Errorf("%w: server reflection does not know %s", ErrUnknownMethod, service)
	}
	if err != nil {
		return nil, fmt.Errorf("server reflection: %w", err)
	}
	return buildFiles(fds)
}

// reflectStream is one server reflection conversation.
type reflectStream interface {
	open(ctx context.Context) error
	// files returns the serialized file descriptors answering a request
	// for a symbol (bySymbol) or a file name.
	files(name string, bySymbol bool) ([][]byte, error)
	close()
}

func reflectFiles(ctx context.Context, s reflectStream, service string) ([]*descriptorpb.FileDescriptorProto, error) {
	if err := s.open(ctx); err != nil {
		return nil, err
	}
	defer s.close()
	seen := map[string]*descriptorpb.FileDescriptorProto{}
	var order []*descriptorpb.FileDescriptorProto
	add := func(raw [][]byte) error {
		for _, b := range raw {
			fd := &descriptorpb.FileDescriptorProto{}
			if err := proto.Unmarshal(b, fd); err != nil {
				return err
			}
			if _, ok := seen[fd.GetName()]; !ok {
				seen[fd.GetName()] = fd
				order = append(order, fd)
			}
		}
		return nil
	}
	raw, err := s.files(service, true)
	if err != nil {
		return nil, err
	}
	if err := add(raw); err != nil {
		return nil, err
	}
	// Servers normally send every dependency with the first answer; ask
	// for any that are missing and not built into this binary.
	for i := 0; i < len(order); i++ {
		for _, dep := range order[i].GetDependency() {
			if _, ok := seen[dep]; ok {
				continue
			}
			if _, err := protoregistry.GlobalFiles.FindFileByPath(dep); err == nil {
				continue
			}
			raw, err := s.files(dep, false)
			if err != nil {
				return nil, err
			}
			if err := add(raw); err != nil {
				return nil, err
			}
		}
	}
	return order, nil
}

type v1Stream struct {
	client reflectv1.ServerReflectionClient
	stream reflectv1.ServerReflection_ServerReflectionInfoClient
	cancel context.CancelFunc
}

func (s *v1Stream) open(ctx context.Context) (err error) {
	ctx, s.cancel = context.WithCancel(ctx)
	s.stream, err = s.client.ServerReflectionInfo(ctx)
	return err
}

func (s *v1Stream) files(name string, bySymbol bool) ([][]byte, error) {
	req := &reflectv1.ServerReflectionRequest{MessageRequest: &reflectv1.ServerReflectionRequest_FileByFilename{FileByFilename: name}}
	if bySymbol {
		req.MessageRequest = &reflectv1.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: name}
	}
	if err := s.stream.Send(req); err != nil {
		return nil, err
	}
	resp, err := s.stream.Recv()
	if err != nil {
		return nil, err
	}
	if e := resp.GetErrorResponse(); e != nil {
		return nil, status.Error(codes.Code(e.GetErrorCode()), e.GetErrorMessage()) //nolint:gosec // status codes are small
	}
	return resp.GetFileDescriptorResponse().GetFileDescriptorProto(), nil
}

func (s *v1Stream) close() {
	_ = s.stream.CloseSend()
	s.cancel()
}

type v1alphaStream struct {
	client reflectv1alpha.ServerReflectionClient
	stream reflectv1alpha.ServerReflection_ServerReflectionInfoClient
	cancel context.CancelFunc
}

func (s *v1alphaStream) open(ctx context.Context) (err error) {
	ctx, s.cancel = context.WithCancel(ctx)
	s.stream, err = s.client.ServerReflectionInfo(ctx)
	return err
}

// files is v1Stream.files for servers that only offer the deprecated
// v1alpha reflection API, which many non-Go servers still do.
//
//nolint:staticcheck // v1alpha is deprecated but still widely deployed
func (s *v1alphaStream) files(name string, bySymbol bool) ([][]byte, error) {
	req := &reflectv1alpha.ServerReflectionRequest{MessageRequest: &reflectv1alpha.ServerReflectionRequest_FileByFilename{FileByFilename: name}}
	if bySymbol {
		req.MessageRequest = &reflectv1alpha.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: name}
	}
	if err := s.stream.Send(req); err != nil {
		return nil, err
	}
	resp, err := s.stream.Recv()
	if err != nil {
		return nil, err
	}
	if e := resp.GetErrorResponse(); e != nil {
		return nil, status.Error(codes.Code(e.GetErrorCode()), e.GetErrorMessage()) //nolint:gosec // status codes are small
	}
	return resp.GetFileDescriptorResponse().GetFileDescriptorProto(), nil
}

func (s *v1alphaStream) close() {
	_ = s.stream.CloseSend()
	s.cancel()
}
