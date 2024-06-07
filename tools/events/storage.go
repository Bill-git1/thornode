package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Store will serialize an object to storage.
func Store(path string, obj any) error {
	path = filepath.Join(config.StoragePath, path)

	dir := filepath.Dir(path)
	err := os.MkdirAll(dir, 0o755)
	if err != nil {
		return err
	}

	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	err = encoder.Encode(obj)
	if err != nil {
		return err
	}

	return nil
}

// Load will load an object from storage.
func Load(path string, obj any) error {
	path = filepath.Join(config.StoragePath, path)

	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	err = decoder.Decode(obj)
	if err != nil {
		return err
	}

	return nil
}
