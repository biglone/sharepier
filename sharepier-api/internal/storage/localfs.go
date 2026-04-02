package storage

import (
	"context"
	"os"
	"path/filepath"
)

type LocalFSStore struct {
	root string
}

func NewLocalFSStore(root string) (*LocalFSStore, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}

	return &LocalFSStore{root: root}, nil
}

func (s *LocalFSStore) Backend() string {
	return "local"
}

func (s *LocalFSStore) Root() string {
	return s.root
}

func (s *LocalFSStore) Location() string {
	return filepath.Join(s.root, "objects")
}

func (s *LocalFSStore) PutFile(_ context.Context, tempPath, key, _ string) error {
	source, err := os.Open(tempPath)
	if err != nil {
		return err
	}
	defer source.Close()

	targetPath := s.objectPath(key)
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}

	target, err := os.Create(targetPath)
	if err != nil {
		return err
	}

	if _, err := target.ReadFrom(source); err != nil {
		target.Close()
		return err
	}

	if err := target.Close(); err != nil {
		return err
	}

	if err := os.Remove(tempPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

func (s *LocalFSStore) Open(_ context.Context, key string) (ReadSeekCloser, error) {
	return os.Open(s.objectPath(key))
}

func (s *LocalFSStore) Delete(_ context.Context, key string) error {
	err := os.Remove(s.objectPath(key))
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

func (s *LocalFSStore) objectPath(key string) string {
	return filepath.Join(s.root, "objects", filepath.FromSlash(key))
}
