package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Store will serialize an object to storage.
func Store(path string, obj any) error {
	path = filepath.Join(config.StoragePath, path)

	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	if err := encoder.Encode(obj); err != nil {
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
	if err := decoder.Decode(obj); err != nil {
		return err
	}

	return nil
}
