// Command stampede-plugin-kafka is Stampede's Kafka plugin: virtual users
// produce records and consume them, in consumer groups or straight from
// partitions.
package main

import "github.com/Ivan825/Stampede/pkg/pluginsdk"

func main() { pluginsdk.Serve(newPlugin()) }
