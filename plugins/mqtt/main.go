// Command stampede-plugin-mqtt is Stampede's MQTT plugin: each virtual
// user is an MQTT client that connects, publishes, subscribes and waits
// for messages.
package main

import "github.com/Ivan825/Stampede/pkg/pluginsdk"

func main() { pluginsdk.Serve(newPlugin()) }
