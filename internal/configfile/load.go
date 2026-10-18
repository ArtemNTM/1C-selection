package configfile

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"
)

func Load(path string, required bool, destination any) error {
	file, err := os.Open(path)
	if err != nil {
		if !required && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open config %q: %w", path, err)
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(destination); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("decode config %q: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return fmt.Errorf("decode config %q: %w", path, err)
		}
		return fmt.Errorf("config %q must contain exactly one YAML document", path)
	}
	return nil
}

func Override[T any](flags *pflag.FlagSet, name string, destination *T, value T) {
	if flags.Changed(name) {
		*destination = value
	}
}
