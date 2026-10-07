package grpcx

import (
	"context"
	"sort"

	"github.com/bufbuild/protocompile"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// CompileSources compiles .proto sources held in memory, keyed by the
// file name imports use. Imports not among the sources are the built-in
// well-known types or, when importPaths is set, files found under those
// directories.
func CompileSources(ctx context.Context, sources map[string][]byte, importPaths []string) (*protoregistry.Files, error) {
	srcs := make(map[string]string, len(sources))
	names := make([]string, 0, len(sources))
	for name, b := range sources {
		srcs[name] = string(b)
		names = append(names, name)
	}
	sort.Strings(names)
	fromMap := protocompile.SourceAccessorFromMap(srcs)
	var resolver protocompile.Resolver = &protocompile.SourceResolver{Accessor: fromMap}
	if len(importPaths) > 0 {
		resolver = protocompile.CompositeResolver{resolver, &protocompile.SourceResolver{ImportPaths: importPaths}}
	}
	c := protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(resolver),
		// Comments are kept for the method summaries the generator shows.
		SourceInfoMode: protocompile.SourceInfoStandard,
	}
	compiled, err := c.Compile(ctx, names...)
	if err != nil {
		return nil, err
	}
	return registerFiles(compiled)
}

// FindMethod finds a callable method (unary or server streaming) in
// compiled descriptors.
func FindMethod(files *protoregistry.Files, service, method string) (protoreflect.MethodDescriptor, error) {
	return findMethod(files, service, method)
}
