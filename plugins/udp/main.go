// Command stampede-plugin-udp is Stampede's UDP plugin: it sends
// datagrams and optionally waits for a reply, timing the round trip.
package main

import "github.com/Ivan825/Stampede/pkg/pluginsdk"

func main() { pluginsdk.Serve(newPlugin()) }
