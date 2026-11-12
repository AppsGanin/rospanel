// Command openapi writes the panel's generated OpenAPI document to a file, so the
// docs site can render it with Swagger UI without a running panel.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/AppsGanin/rospanel/internal/server"
)

func main() {
	out := flag.String("out", "docs/openapi.json", "output file")
	base := flag.String("server", "http://localhost:8080", "server URL written into the spec")
	flag.Parse()

	body, err := server.OpenAPIDocument(*base)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, body, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d bytes)\n", *out, len(body))
}
