package auth_test

import (
	"testing"
	"time"

	"github.com/Audi-dask/Overseer/internal/auth"
)

func TestPasswordAndJWT(t *testing.T) {
	hash, err := auth.HashPassword("secret1")
	if err != nil {
		t.Fatal(err)
	}
	if !auth.CheckPassword(hash, "secret1") {
		t.Fatal("password should match")
	}
	if auth.CheckPassword(hash, "wrong") {
		t.Fatal("wrong password should fail")
	}

	svc := auth.New([]byte("test-jwt-key-0123456789abcdef"))
	token, exp, err := svc.IssueToken("admin")
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(exp) < 24*time.Hour {
		t.Fatalf("unexpected expiry: %v", exp)
	}
	claims, err := svc.ParseToken("Bearer " + token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Username != "admin" {
		t.Fatalf("username=%q", claims.Username)
	}
}
