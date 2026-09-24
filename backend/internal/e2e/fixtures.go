//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// FixturePath returns the absolute path to a testdata fixture directory.
func FixturePath(name string) string {
	// Resolve relative to this source file's directory.
	return filepath.Join(testdataDir(), name)
}

func testdataDir() string {
	// Heuristic: walk up from cwd to find internal/e2e/testdata.
	dir, _ := os.Getwd()
	for {
		candidate := filepath.Join(dir, "internal", "e2e", "testdata")
		if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	// Fallback: relative from typical `go test` cwd.
	return "testdata"
}

// GenerateGoProject creates a minimal Go project in a temp directory with
// handler/service/repository layered architecture for testing CKG, Constraints,
// and QualityGate features.
func GenerateGoProject(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "wescode-e2e-goproj-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	writeFile(t, dir, "go.mod", `module example.com/testapp

go 1.22
`)
	writeFile(t, dir, "main.go", `package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`)
	// Layered architecture
	os.MkdirAll(filepath.Join(dir, "internal", "handler"), 0755)
	os.MkdirAll(filepath.Join(dir, "internal", "service"), 0755)
	os.MkdirAll(filepath.Join(dir, "internal", "repository"), 0755)

	writeFile(t, dir, "internal/handler/auth.go", `package handler

import "example.com/testapp/internal/service"

type AuthHandler struct {
	svc *service.AuthService
}

func NewAuthHandler(svc *service.AuthService) *AuthHandler {
	return &AuthHandler{svc: svc}
}

func (h *AuthHandler) HandleLogin(username, password string) (string, error) {
	return h.svc.Login(username, password)
}
`)
	writeFile(t, dir, "internal/service/auth.go", `package service

import "example.com/testapp/internal/repository"

type AuthService struct {
	repo *repository.UserRepository
}

func NewAuthService(repo *repository.UserRepository) *AuthService {
	return &AuthService{repo: repo}
}

func (s *AuthService) Login(username, password string) (string, error) {
	user, err := s.repo.FindByUsername(username)
	if err != nil {
		return "", err
	}
	if user.Password != password {
		return "", ErrInvalidCredentials
	}
	return "token-" + user.ID, nil
}

func ValidateEmail(email string) bool {
	return len(email) > 3 && contains(email, "@") && contains(email, ".")
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

var ErrInvalidCredentials = &AuthError{Message: "invalid credentials"}

type AuthError struct{ Message string }

func (e *AuthError) Error() string { return e.Message }
`)
	writeFile(t, dir, "internal/repository/user.go", `package repository

type User struct {
	ID       string
	Username string
	Password string
	Email    string
}

type UserRepository struct {
	users map[string]*User
}

func NewUserRepository() *UserRepository {
	return &UserRepository{users: map[string]*User{
		"alice": {ID: "1", Username: "alice", Password: "secret", Email: "alice@example.com"},
		"bob":   {ID: "2", Username: "bob", Password: "pass123", Email: "bob@example.com"},
	}}
}

func (r *UserRepository) FindByUsername(username string) (*User, error) {
	u, ok := r.users[username]
	if !ok {
		return nil, &NotFoundError{Entity: "user", ID: username}
	}
	return u, nil
}

type NotFoundError struct {
	Entity string
	ID     string
}

func (e *NotFoundError) Error() string { return e.Entity + " not found: " + e.ID }
`)
	writeFile(t, dir, "internal/service/auth_test.go", `package service

import "testing"

func TestValidateEmail(t *testing.T) {
	tests := []struct {
		email string
		want  bool
	}{
		{"alice@example.com", true},
		{"invalid", false},
		{"a@b.c", true},
		{"", false},
	}
	for _, tt := range tests {
		if got := ValidateEmail(tt.email); got != tt.want {
			t.Errorf("ValidateEmail(%q) = %v, want %v", tt.email, got, tt.want)
		}
	}
}

func TestLogin_Success(t *testing.T) {
	// This test requires repository — placeholder for integration test.
	t.Skip("requires repository stub")
}
`)

	return dir
}

func writeFile(t *testing.T, base, rel, content string) {
	t.Helper()
	path := filepath.Join(base, rel)
	os.MkdirAll(filepath.Dir(path), 0755)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
