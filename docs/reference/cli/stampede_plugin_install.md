## stampede plugin install

Build and install a plugin

### Synopsis

Installs a plugin into the plugin directory as stampede-plugin-<name>:

  stampede plugin install mqtt                 a first-party plugin (mqtt, kafka, redis, sql, udp),
                                               built from plugins/<name> of a Stampede checkout
  stampede plugin install ./my-plugin          a directory with a plugin's main package (go build)
  stampede plugin install ./stampede-plugin-x  a built plugin executable (copied)
  stampede plugin install example.com/x@v1.2.0 a Go package (go install)

Building needs a Go toolchain. A first-party plugin is built from the
checkout given by --source or $STAMPEDE_SOURCE, or the one containing the
current directory; with none, the repository is cloned (git is needed).
The plugin is started and must describe itself correctly before it is
installed.

```
stampede plugin install <name | directory | file | go-package[@version]> [flags]
```

### Options

```
  -h, --help            help for install
      --ref string      branch or tag to clone when there is no checkout
      --source string   Stampede source checkout to build first-party plugins from
```

### Options inherited from parent commands

```
      --dir string   plugin directory (default $STAMPEDE_PLUGIN_DIR or the user config directory)
```

### SEE ALSO

* [stampede plugin](stampede_plugin.md)	 - List, install and remove protocol plugins

