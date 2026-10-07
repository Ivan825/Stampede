## stampede plugin create

Scaffold a plugin: a Go module with one step, a README and a conformance test

### Synopsis

Creates <dest>/stampede-plugin-<name>, a Go module built on pkg/pluginsdk
with one working step (<name>.send: write a line to a TCP server and time
the reply line), a README, and a test that starts a local TCP server and
runs the SDK's conformance suite against the built plugin. Replace the
step with your protocol and keep the test passing.

The module depends on github.com/Ivan825/Stampede at this binary's
version (the latest one for a development build). With --sdk it builds
against a Stampede checkout instead, through a replace directive, as the
first-party plugins do. go mod tidy runs at the end when Go is installed.

```
stampede plugin create <name> [flags]
```

### Examples

```
  stampede plugin create smtp
  cd stampede-plugin-smtp && go test ./... && stampede plugin install .
  stampede plugin create smtp --module github.com/you/stampede-plugin-smtp --sdk ~/src/Stampede
```

### Options

```
      --dest string     folder to create the plugin in (default ".")
  -h, --help            help for create
      --module string   Go module path (default example.com/stampede-plugin-<name>)
      --no-tidy         do not run go get and go mod tidy
      --sdk string      a Stampede source checkout to build against (adds a replace directive)
```

### Options inherited from parent commands

```
      --dir string   plugin directory (default $STAMPEDE_PLUGIN_DIR or the user config directory)
```

### SEE ALSO

* [stampede plugin](stampede_plugin.md)	 - List, install, remove and create protocol plugins

