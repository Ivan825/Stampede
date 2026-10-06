// Command stampede-plugin-sql is Stampede's SQL plugin: it runs queries
// and statements against PostgreSQL, MySQL and SQLite.
package main

import "github.com/Ivan825/Stampede/pkg/pluginsdk"

func main() { pluginsdk.Serve(newPlugin()) }
