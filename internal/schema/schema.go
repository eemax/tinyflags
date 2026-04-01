package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"os"

	"github.com/santhosh-tekuri/jsonschema/v6"

	cerr "github.com/eemax/tinyflags/internal/errors"
)

func Load(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, cerr.Wrap(cerr.ExitSchemaValidation, "read output schema", err)
	}
	return data, nil
}

func Validate(ctx context.Context, schemaBytes []byte, text string) (json.RawMessage, error) {
	if len(schemaBytes) == 0 {
		return nil, nil
	}

	type result struct {
		data json.RawMessage
		err  error
	}
	done := make(chan result, 1)
	go func() {
		compiler := jsonschema.NewCompiler()
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaBytes))
		if err != nil {
			done <- result{err: cerr.Wrap(cerr.ExitSchemaValidation, "parse JSON schema", err)}
			return
		}
		if err := compiler.AddResource("schema.json", doc); err != nil {
			done <- result{err: cerr.Wrap(cerr.ExitSchemaValidation, "load JSON schema", err)}
			return
		}
		s, err := compiler.Compile("schema.json")
		if err != nil {
			done <- result{err: cerr.Wrap(cerr.ExitSchemaValidation, "compile JSON schema", err)}
			return
		}
		var payload any
		if err := json.Unmarshal([]byte(text), &payload); err != nil {
			done <- result{err: cerr.Wrap(cerr.ExitSchemaValidation, "parse model output as JSON", err)}
			return
		}
		if err := s.Validate(payload); err != nil {
			done <- result{err: cerr.Wrap(cerr.ExitSchemaValidation, "validate model output against schema", err)}
			return
		}
		out, err := json.Marshal(payload)
		if err != nil {
			done <- result{err: cerr.Wrap(cerr.ExitSchemaValidation, "marshal validated JSON", err)}
			return
		}
		done <- result{data: out}
	}()

	select {
	case <-ctx.Done():
		if ctx.Err() == context.DeadlineExceeded {
			return nil, cerr.New(cerr.ExitTimeout, "invocation timed out")
		}
		return nil, ctx.Err()
	case result := <-done:
		return result.data, result.err
	}
}
