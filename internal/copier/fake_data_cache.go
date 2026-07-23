package copier

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	fakeDataCacheDirName  = ".mssql-copier"
	fakeDataCacheFileName = "fake-data-mapping.yml"
)

var executablePath = os.Executable

type persistedFakeDataMappings struct {
	Sources  map[string][]persistedFakeDataEntry `yaml:"sources,omitempty"`
	Mappings map[string]map[string]string        `yaml:"mappings,omitempty"`
}

type persistedFakeDataEntry struct {
	Selector        string   `yaml:"selector,omitempty"`
	Display         string   `yaml:"display,omitempty"`
	TypeName        string   `yaml:"type-name,omitempty"`
	FunctionName    string   `yaml:"function-name,omitempty"`
	FunctionDisplay string   `yaml:"function-display,omitempty"`
	FunctionParams  []string `yaml:"function-params,omitempty"`
	RequireUnique   bool     `yaml:"require-unique,omitempty"`
}

func fakeDataCachePath() (string, error) {
	executable, err := executablePath()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	return filepath.Join(filepath.Dir(executable), fakeDataCacheDirName, fakeDataCacheFileName), nil
}

func loadCachedFakeDataEntries(sourceDSN string) ([]tuiFakeDataEntry, bool, error) {
	cacheKey := fakeDataCacheKey(sourceDSN)
	if cacheKey == "" {
		return nil, false, nil
	}

	path, err := fakeDataCachePath()
	if err != nil {
		return nil, false, err
	}
	// #nosec G304 -- path is resolved under the local executable directory for app-managed cache data.
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read fake-data cache %q: %w", path, err)
	}

	var persisted persistedFakeDataMappings
	if err := yaml.Unmarshal(raw, &persisted); err != nil {
		return nil, false, fmt.Errorf("parse fake-data cache %q: %w", path, err)
	}
	entries, ok := persisted.Sources[cacheKey]
	if !ok {
		return nil, false, nil
	}
	return decodePersistedFakeDataEntries(entries), true, nil
}

func loadCachedFakeDataMappings(sourceDSN string) (map[string]string, bool, error) {
	cacheKey := fakeDataCacheKey(sourceDSN)
	if cacheKey == "" {
		return nil, false, nil
	}

	path, err := fakeDataCachePath()
	if err != nil {
		return nil, false, err
	}
	// #nosec G304 -- path is resolved under the local executable directory for app-managed cache data.
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read fake-data cache %q: %w", path, err)
	}

	var persisted persistedFakeDataMappings
	if err := yaml.Unmarshal(raw, &persisted); err != nil {
		return nil, false, fmt.Errorf("parse fake-data cache %q: %w", path, err)
	}
	if persisted.Mappings != nil {
		if mappings, ok := persisted.Mappings[cacheKey]; ok {
			return cloneStringMap(mappings), true, nil
		}
	}
	entries, ok := persisted.Sources[cacheKey]
	if !ok {
		return nil, false, nil
	}
	mappings := make(map[string]string)
	for _, entry := range entries {
		if strings.TrimSpace(entry.FunctionName) == "" {
			continue
		}
		mappings[entry.Selector] = buildFakeFunctionConfig(entry.FunctionName, entry.FunctionParams)
	}
	if len(mappings) == 0 {
		return nil, true, nil
	}
	return mappings, true, nil
}

func loadCachedFakeDataUnique(sourceDSN string) (map[string]bool, bool, error) {
	cacheKey := fakeDataCacheKey(sourceDSN)
	if cacheKey == "" {
		return nil, false, nil
	}

	path, err := fakeDataCachePath()
	if err != nil {
		return nil, false, err
	}
	// #nosec G304 -- path is resolved under the local executable directory for app-managed cache data.
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read fake-data cache %q: %w", path, err)
	}

	var persisted persistedFakeDataMappings
	if err := yaml.Unmarshal(raw, &persisted); err != nil {
		return nil, false, fmt.Errorf("parse fake-data cache %q: %w", path, err)
	}
	entries, ok := persisted.Sources[cacheKey]
	if !ok {
		return nil, false, nil
	}

	unique := make(map[string]bool)
	for _, entry := range entries {
		if entry.RequireUnique {
			unique[entry.Selector] = true
		}
	}
	if len(unique) == 0 {
		return nil, true, nil
	}
	return unique, true, nil
}

func saveCachedFakeDataEntries(sourceDSN string, entries []tuiFakeDataEntry) error {
	cacheKey := fakeDataCacheKey(sourceDSN)
	if cacheKey == "" {
		return nil
	}

	path, err := fakeDataCachePath()
	if err != nil {
		return err
	}

	persisted := persistedFakeDataMappings{}
	// #nosec G304 -- path is resolved under the local executable directory for app-managed cache data.
	raw, err := os.ReadFile(path)
	if err == nil {
		if unmarshalErr := yaml.Unmarshal(raw, &persisted); unmarshalErr != nil {
			return fmt.Errorf("parse fake-data cache %q: %w", path, unmarshalErr)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read fake-data cache %q: %w", path, err)
	}

	if persisted.Sources == nil {
		persisted.Sources = make(map[string][]persistedFakeDataEntry)
	}
	persisted.Sources[cacheKey] = encodePersistedFakeDataEntries(entries)

	encoded, err := yaml.Marshal(persisted)
	if err != nil {
		return fmt.Errorf("marshal fake-data cache: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create fake-data cache dir %q: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return fmt.Errorf("write fake-data cache %q: %w", path, err)
	}
	return nil
}

func saveCachedFakeDataMappings(sourceDSN string, mappings map[string]string) error {
	cacheKey := fakeDataCacheKey(sourceDSN)
	if cacheKey == "" {
		return nil
	}

	path, err := fakeDataCachePath()
	if err != nil {
		return err
	}

	persisted := persistedFakeDataMappings{}
	// #nosec G304 -- path is resolved under the local executable directory for app-managed cache data.
	raw, err := os.ReadFile(path)
	if err == nil {
		if unmarshalErr := yaml.Unmarshal(raw, &persisted); unmarshalErr != nil {
			return fmt.Errorf("parse fake-data cache %q: %w", path, unmarshalErr)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read fake-data cache %q: %w", path, err)
	}

	if persisted.Mappings == nil {
		persisted.Mappings = make(map[string]map[string]string)
	}
	if len(mappings) == 0 {
		delete(persisted.Mappings, cacheKey)
	} else {
		persisted.Mappings[cacheKey] = cloneStringMap(mappings)
	}

	encoded, err := yaml.Marshal(persisted)
	if err != nil {
		return fmt.Errorf("marshal fake-data cache: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create fake-data cache dir %q: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return fmt.Errorf("write fake-data cache %q: %w", path, err)
	}
	return nil
}

func encodePersistedFakeDataEntries(entries []tuiFakeDataEntry) []persistedFakeDataEntry {
	persisted := make([]persistedFakeDataEntry, 0, len(entries))
	for _, entry := range entries {
		persisted = append(persisted, persistedFakeDataEntry{
			Selector:        entry.Selector,
			Display:         entry.Display,
			TypeName:        entry.TypeName,
			FunctionName:    entry.FunctionName,
			FunctionDisplay: entry.FunctionDisplay,
			FunctionParams:  append([]string(nil), entry.FunctionParams...),
			RequireUnique:   entry.RequireUnique,
		})
	}
	return persisted
}

func decodePersistedFakeDataEntries(entries []persistedFakeDataEntry) []tuiFakeDataEntry {
	decoded := make([]tuiFakeDataEntry, 0, len(entries))
	for _, entry := range entries {
		decoded = append(decoded, tuiFakeDataEntry{
			Selector:        entry.Selector,
			Display:         entry.Display,
			TypeName:        entry.TypeName,
			FunctionName:    entry.FunctionName,
			FunctionDisplay: entry.FunctionDisplay,
			FunctionParams:  append([]string(nil), entry.FunctionParams...),
			RequireUnique:   entry.RequireUnique,
		})
	}
	return decoded
}

func fakeDataCacheKey(sourceDSN string) string {
	trimmedDSN := strings.TrimSpace(sourceDSN)
	if trimmedDSN == "" {
		return ""
	}

	form := parseSQLServerDSNForm(trimmedDSN)
	cacheKey, err := buildSQLServerDSN(sqlServerDSNForm{
		Server:   form.Server,
		Port:     form.Port,
		Database: form.Database,
	})
	if err != nil {
		return ""
	}
	return cacheKey
}
