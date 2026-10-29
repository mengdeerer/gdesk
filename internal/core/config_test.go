package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAddHostPersistAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	config := Config{Interval: 5, Timeout: 12}
	for _, alias := range []string{"atlas-gpu", "nebula-gpu"} {
		var err error
		config, err = AddHost(path, config, alias)
		if err != nil {
			t.Fatal(err)
		}
	}
	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Hosts) != 2 || reloaded.Hosts[0].SSH != "atlas-gpu" || reloaded.Hosts[1].SSH != "nebula-gpu" {
		t.Fatalf("wrong saved aliases: %+v", reloaded.Hosts)
	}
	if _, err = AddHost(path, config, "nebula-gpu"); err == nil {
		t.Fatal("duplicate alias accepted")
	}
}
func TestAddHostRejectsCommandsWithoutWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	config := Config{Interval: 5, Timeout: 12}
	for _, alias := range []string{"", "ssh atlas-gpu", "-oProxyCommand=bad", "host;echo bad", "$(echo bad)", "host\nother"} {
		if _, err := AddHost(path, config, alias); err == nil {
			t.Fatalf("invalid alias accepted: %q", alias)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("invalid input wrote config")
	}
}
func TestAddHostPreservesUnknownFieldsAndExternalChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := []byte(`{"custom":"keep", "hosts":[{"id":"old","ssh":"old","custom_host":"keep"}]}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	next, err := AddHost(path, config, "nebula-gpu")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err = json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["custom"] != "keep" || raw["hosts"].([]any)[0].(map[string]any)["custom_host"] != "keep" {
		t.Fatal("unrecognized config fields were lost")
	}
	changed := []byte(`{"interval_seconds":10,"hosts":[]}`)
	if err = os.WriteFile(path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = AddHost(path, next, "atlas-gpu"); err == nil {
		t.Fatal("overwrote external changes")
	}
	actual, _ := os.ReadFile(path)
	if string(actual) != string(changed) {
		t.Fatal("file mutated despite conflict")
	}
}
func TestEmptyConfigurationAllowedForOnboarding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"hosts":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil || len(config.Hosts) != 0 {
		t.Fatalf("empty setup invalid: %v", err)
	}
}

func TestRemoveHostPersistsEmptyListAndPreservesOtherFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := []byte(`{"custom":"keep","hosts":[{"id":"first","ssh":"first"},{"id":"second","ssh":"second","other":"keep"}]}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	config, err = RemoveHost(path, config, "first")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var raw map[string]any
	if err = json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["custom"] != "keep" || raw["hosts"].([]any)[0].(map[string]any)["other"] != "keep" {
		t.Fatal("removal changed unrelated fields")
	}
	config, err = RemoveHost(path, config, "second")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(path)
	if err != nil || len(loaded.Hosts) != 0 {
		t.Fatalf("last host was not removed: %v", err)
	}
	if _, err = AddHost(path, config, "first"); err != nil {
		t.Fatalf("cannot add after removing last host: %v", err)
	}
}
func TestRemoveHostRejectsExternalChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	config, err := AddHost(path, Config{Interval: 5, Timeout: 12}, "atlas-gpu")
	if err != nil {
		t.Fatal(err)
	}
	changed := []byte(`{"hosts":[],"custom":"new"}`)
	if err = os.WriteFile(path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = RemoveHost(path, config, "atlas-gpu"); err == nil {
		t.Fatal("removal overwrote externally modified config")
	}
	data, _ := os.ReadFile(path)
	if string(data) != string(changed) {
		t.Fatal("external edits were lost")
	}
}
