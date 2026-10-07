package scenario

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Parse decodes and validates a scenario from YAML or JSON.
func Parse(src []byte) (*Scenario, error) {
	s, err := Decode(src)
	if err != nil {
		return nil, err
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return s, nil
}

// Decode reads a scenario without semantic validation, rejecting unknown
// fields. Defaults are applied.
func Decode(src []byte) (*Scenario, error) {
	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)
	var s Scenario
	if err := dec.Decode(&s); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("scenario is empty")
		}
		return nil, fmt.Errorf("parse scenario: %s", cleanYAMLError(err))
	}
	s.applyDefaults()
	return &s, nil
}

// LoadFile reads a scenario file. Relative feeder paths are resolved against
// the file's directory.
func LoadFile(path string) (*Scenario, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	s.ResolvePaths(filepath.Dir(path))
	return s, nil
}

// ResolvePaths makes relative feeder and gRPC descriptor file paths
// relative to dir.
func (s *Scenario) ResolvePaths(dir string) {
	abs := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(dir, p)
	}
	for name, f := range s.Data {
		f.CSV, f.JSON = abs(f.CSV), abs(f.JSON)
		s.Data[name] = f
	}
	if r := s.Load.Replay; r != nil {
		r.File = abs(r.File)
	}
	for _, j := range s.Journeys {
		walkSteps(j.Steps, func(st Step) {
			for _, ch := range st.Checks() {
				if p, ok := ch.Schema.(string); ok {
					ch.Schema = abs(p)
				}
			}
			if g := st.GRPC; g != nil {
				g.Protoset = abs(g.Protoset)
				// .proto files are found under the import paths, so only
				// the import paths move; with none, the file's directory
				// becomes the import path.
				if len(g.Proto) > 0 && len(g.ImportPaths) == 0 {
					g.ImportPaths = []string{dir}
				} else {
					for i, p := range g.ImportPaths {
						g.ImportPaths[i] = abs(p)
					}
				}
			}
		})
	}
}

// Marshal renders the scenario as YAML.
func (s *Scenario) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(s); err != nil {
		return nil, err
	}
	return buf.Bytes(), enc.Close()
}

func (s *Scenario) applyDefaults() {
	if s.APIVersion == "" {
		s.APIVersion = APIVersion
	}
	if s.Kind == "" {
		s.Kind = KindScenario
	}
	if s.Target.Timeout == 0 {
		s.Target.Timeout = Duration(30e9)
	}
	if s.Target.HTTP.Connections == "" {
		s.Target.HTTP.Connections = "per-vu"
	}
	for i := range s.Journeys {
		if s.Journeys[i].Weight == 0 {
			s.Journeys[i].Weight = 1
		}
	}
	for name, f := range s.Data {
		if f.Mode == "" {
			f.Mode = FeedSequential
		}
		if f.OnExhausted == "" {
			f.OnExhausted = "stop"
		}
		s.Data[name] = f
	}
	if s.Load.Mode == "" {
		if s.Load.Rate > 0 {
			s.Load.Mode = ModeRate
		} else {
			s.Load.Mode = ModeVUs
		}
	}
	if s.Load.GracefulStop == 0 {
		s.Load.GracefulStop = Duration(30e9)
	}
}

func cleanYAMLError(err error) string {
	msg := err.Error()
	msg = strings.TrimPrefix(msg, "yaml: ")
	msg = strings.ReplaceAll(msg, "unmarshal errors:\n  ", "")
	msg = strings.ReplaceAll(msg, "in type scenario.", "in ")
	return msg
}
