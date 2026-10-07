package main

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"
)

const sample = `openapi: 3.0.3
info: { title: Test API, version: 1.2.3, description: A test. }
servers: [{ url: /api/v1 }]
tags: [{ name: runs, description: Runs. }, { name: system }]
paths:
  /version:
    get:
      tags: [system]
      operationId: getVersion
      security: []
      responses:
        "200":
          description: Version
          content:
            application/json:
              schema: { $ref: "#/components/schemas/Version" }
  /runs/{runId}:
    parameters:
      - $ref: "#/components/parameters/runId"
    post:
      tags: [runs]
      operationId: updateRun
      summary: Change a run
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [note]
              properties:
                note: { type: string, description: "A | note" }
                env: { type: object, additionalProperties: { type: string } }
      responses:
        "404": { $ref: "#/components/responses/NotFound" }
        "200":
          description: The run
          content:
            application/json:
              schema: { $ref: "#/components/schemas/Run" }
    get:
      tags: [runs]
      operationId: getRun
      parameters:
        - { name: format, in: query, schema: { type: string, enum: [json, html], default: json } }
      responses:
        "200":
          description: The run
          content:
            application/json:
              schema: { type: array, items: { $ref: "#/components/schemas/Run" } }
components:
  securitySchemes:
    bearer: { type: http, scheme: bearer, description: API token }
  parameters:
    runId: { name: runId, in: path, required: true, schema: { type: string, format: uuid } }
  responses:
    NotFound:
      description: Not found
      content:
        application/json:
          schema: { $ref: "#/components/schemas/Error" }
  schemas:
    Error:
      type: object
      properties:
        error:
          type: object
          required: [code]
          properties:
            code: { type: string }
    Version:
      type: object
      required: [version]
      properties:
        version: { type: string }
    Run:
      type: object
      properties:
        id: { type: string, format: uuid }
        verdict: { type: string, nullable: true, enum: [pass, fail, null] }
    RunWithReport:
      allOf:
        - $ref: "#/components/schemas/Run"
        - type: object
          properties:
            report: { type: string }
`

func TestRender(t *testing.T) {
	var b bytes.Buffer
	if err := render(&b, []byte(sample), "test.yaml"); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"## Runs\n\nRuns.\n\n### GET /runs/{runId}\n",
		// Methods in a fixed order, whatever the document's order.
		"### GET /runs/{runId}",
		"| `runId` | path | string (uuid) | yes |  |",
		"| `format` | query | string: `json`, `html` | no | Default `json`. |",
		"**Request body** (required): `application/json` object",
		"| `note` | string | yes | A \\| note |",
		"| `env` | map of string | no |  |",
		"| 404 | Not found | `application/json` [Error](#error) |",
		"`application/json` array of [Run](#run)",
		"Operation `getVersion`, **no authentication**.",
		"| `error.code` | string | yes |  |",
		"| `verdict` | string: `pass`, `fail`, nullable | no |  |",
		"Every field of [Run](#run), plus:",
		"[`POST /runs/{runId}`](#post-runsrunid)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Index(out, "### GET /runs/{runId}") > strings.Index(out, "### POST /runs/{runId}") {
		t.Error("GET should come before POST")
	}
	if strings.Index(out, "## Runs") > strings.Index(out, "## System") {
		t.Error("tags should follow the document's order")
	}
	if strings.Index(out, "| 200 | The run") > strings.Index(out, "| 404 |") {
		t.Error("responses should be sorted by status")
	}
	checkLinks(t, out)
}

// checkLinks fails for an in-page link to a heading that does not exist.
func checkLinks(t *testing.T, md string) {
	t.Helper()
	s := slugger{}
	anchors := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^#{1,6} (.+)$`).FindAllStringSubmatch(md, -1) {
		anchors[s.slug(m[1])] = true
	}
	for _, m := range regexp.MustCompile(`\]\(#([^)]+)\)`).FindAllStringSubmatch(md, -1) {
		if !anchors[m[1]] {
			t.Errorf("link to #%s has no heading", m[1])
		}
	}
}

func TestSlug(t *testing.T) {
	s := slugger{}
	for in, want := range map[string]string{
		"GET /runs/{runId}/report": "get-runsrunidreport",
		"POST /runs/kill-all":      "post-runskill-all",
		"Run":                      "run",
		"run":                      "run-1",
	} {
		if got := s.slug(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

// TestCommittedReference checks docs/reference/api.md is current and its
// links resolve.
func TestCommittedReference(t *testing.T) {
	src, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := render(&b, src, "api/openapi.yaml"); err != nil {
		t.Fatal(err)
	}
	have, err := os.ReadFile("../../docs/reference/api.md")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(have, b.Bytes()) {
		t.Error("docs/reference/api.md is stale: run make api-docs")
	}
	checkLinks(t, b.String())
}
