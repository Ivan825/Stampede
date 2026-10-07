package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Ivan825/Stampede/internal/pack"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// packNameRe is what a pack name may look like: it becomes a folder
// name and the pack:<name> scenario tag.
var packNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

func newPackCreateCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Scaffold a new pack folder with a journey, a stress, targets and data",
		Long: `Creates <dir>/<name> with the layout of the built-in packs: pack.yaml,
README.md, journeys/, stresses/, targets.yaml and data/. The journey and the
stress request the target's home page and are valid scenarios from the
start; edit them to follow your product's API, then dry-run them with
stampede pack test <dir>/<name> --target <url>. See docs/guides/packs.md.`,
		Example: `  stampede pack create fintech
  stampede pack test fintech --target http://localhost:8080`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := createPack(dir, args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created %s:\n", root)
			for _, f := range packFiles(args[0]) {
				fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", filepath.FromSlash(f.path))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "\nNext: edit the journeys for your API, then stampede pack test %s --target <url>\n", root)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "folder to create the pack in")
	return cmd
}

// createPack writes the scaffold and checks that it loads as a pack whose
// scenarios are all valid.
func createPack(dir, name string) (string, error) {
	if !packNameRe.MatchString(name) {
		return "", fmt.Errorf("pack name %q: use lowercase letters, digits and hyphens, starting with a letter", name)
	}
	root := filepath.Join(dir, name)
	if _, err := os.Stat(root); err == nil {
		return "", fmt.Errorf("%s already exists", root)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	for _, f := range packFiles(name) {
		p := filepath.Join(root, filepath.FromSlash(f.path))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			return "", err
		}
		if err := os.WriteFile(p, []byte(f.body), 0o600); err != nil {
			return "", err
		}
	}
	p, err := pack.Load(root)
	if err != nil {
		return "", err
	}
	files, err := p.Files()
	if err != nil {
		return "", err
	}
	for _, f := range files {
		if _, err := scenario.LoadFile(filepath.Join(root, filepath.FromSlash(f))); err != nil {
			return "", fmt.Errorf("the scaffold's %s is not valid: %w", f, err)
		}
	}
	return root, nil
}

type packFile struct{ path, body string }

// packFiles is the scaffold of a pack called name.
func packFiles(name string) []packFile {
	title := strings.ToUpper(name[:1]) + strings.ReplaceAll(name[1:], "-", " ")
	r := strings.NewReplacer("{{name}}", name, "{{title}}", title)
	return []packFile{
		{"pack.yaml", r.Replace(packYAML)},
		{"README.md", r.Replace(packREADME)},
		{"journeys/everyday.yaml", r.Replace(packJourney)},
		{"stresses/spike.yaml", r.Replace(packStress)},
		{"targets.yaml", packTargets},
		{"data/searches.csv", "term\nshoes\nlamp\nbook\n"},
	}
}

const packYAML = `apiVersion: stampede.dev/v1
kind: Pack
name: {{name}}
title: {{title}}
description: >
  What kind of product this pack is for and what its journeys and stresses
  check.
# A pack is shipped only when its journeys and stresses run without errors
# against a reference app (see docs/guides/packs.md).
status: planned
protocols: [http]
referenceApp: ""
# How stampede init recognises this kind of product; see "Detection" in
# docs/guides/packs.md.
detect:
  paths: []
  openapiTags: []
  htmlMeta: []
  headers: {}
variables:
  TARGET_URL: { description: Base URL of the system under test }
targets:
  - http.p95 < 500ms
  - errors < 1%
`

const packREADME = `# {{title}} pack

Journeys, stresses and targets for {{title}} products.

| File | What it tests |
|---|---|
| ` + "`journeys/everyday.yaml`" + ` | Everyday use: open the home page, search with a term from ` + "`data/searches.csv`" + ` |
| ` + "`stresses/spike.yaml`" + ` | A sudden tenfold spike on the home page |
| ` + "`targets.yaml`" + ` | Default targets: p95 under 500ms, under 1% errors |

The journeys only request the home page so far. Change the paths, checks
and extractors to follow your product's API, then check that every
journey works with one user:

` + "```sh" + `
stampede pack test {{name}} --target http://localhost:8080
stampede run {{name}}/journeys/everyday.yaml -e TARGET_URL=http://localhost:8080
` + "```" + `
`

const packJourney = `# The everyday mix for this kind of product. Replace the requests with
# your product's API; keep the journeys under two minutes for one user,
# think times included (the dry run's budget per file).
apiVersion: stampede.dev/v1
kind: Scenario
metadata:
  name: {{name}}-everyday
  tags: [pack:{{name}}]
target:
  baseURL: ${env.TARGET_URL}
data:
  searches: { csv: ../data/searches.csv, mode: random }
journeys:
  - name: browse
    weight: 70
    steps:
      - get: /
        check: { status: 200 }
      - think: 1s..2s
  - name: search
    weight: 30
    steps:
      - get: /?q=${data.searches.term}
        check: { status: 200 }
      - think: 1s..2s
load:
  mode: rate
  rate: 10/s
  duration: 2m
targets:
  - http.p95 < 500ms
  - errors < 1%
`

const packStress = `# Traffic jumps tenfold in seconds, holds, then falls back.
apiVersion: stampede.dev/v1
kind: Scenario
metadata:
  name: {{name}}-spike
  tags: [pack:{{name}}, spike]
target:
  baseURL: ${env.TARGET_URL}
journeys:
  - name: home
    steps:
      - get: /
        check: { status: 200 }
load:
  shape: spike
  mode: rate
  start: 10/s
  max: 100/s
  duration: 5m
targets:
  - http.p95 < 800ms
  - errors < 1%
`

const packTargets = `# Default targets for this kind of product.
targets:
  - http.p95 < 500ms
  - errors < 1%
`
