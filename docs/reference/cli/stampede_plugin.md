## stampede plugin

List, install and remove protocol plugins

### Synopsis

Plugins add step types such as mqtt.publish or kafka.produce. Each is an
executable named stampede-plugin-<name>, looked for in the plugin directory
($STAMPEDE_PLUGIN_DIR, or plugins/ under your user config directory) and
then on PATH. Workers find plugins the same way: install a plugin on every
worker that runs scenarios using it.

### Options

```
      --dir string   plugin directory (default $STAMPEDE_PLUGIN_DIR or the user config directory)
  -h, --help         help for plugin
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing
* [stampede plugin install](stampede_plugin_install.md)	 - Build and install a plugin
* [stampede plugin list](stampede_plugin_list.md)	 - List installed plugins and the steps they offer
* [stampede plugin remove](stampede_plugin_remove.md)	 - Remove an installed plugin
* [stampede plugin show](stampede_plugin_show.md)	 - Show a plugin's steps and the settings each takes

