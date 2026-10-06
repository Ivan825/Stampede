// Command stampede-plugin-redis is Stampede's Redis plugin: it runs any
// command, or a pipeline of commands, on each virtual user's own
// connection.
package main

import "github.com/Ivan825/Stampede/pkg/pluginsdk"

func main() { pluginsdk.Serve(newPlugin()) }
