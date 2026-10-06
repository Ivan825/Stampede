package cli

import (
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

// newHealthcheckCmd exists for container health checks: the image has no
// shell or curl.
func newHealthcheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "healthcheck <url>",
		Short:  "Exit 0 when the URL answers 200 (for container health checks)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := &http.Client{Timeout: 3 * time.Second}
			resp, err := c.Get(args[0])
			if err != nil {
				return err
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("%s returned %d", args[0], resp.StatusCode)
			}
			return nil
		},
	}
}
