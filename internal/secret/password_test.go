package secret

import "testing"

func TestPasswordHashAndVerify(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == "" || hash == "correct horse battery staple" {
		t.Fatal("password was not hashed")
	}

	ok, err := VerifyPassword(hash, "correct horse battery staple")
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Fatal("expected password to match")
	}

	ok, err = VerifyPassword(hash, "wrong password")
	if err != nil {
		t.Fatalf("VerifyPassword wrong: %v", err)
	}
	if ok {
		t.Fatal("expected wrong password to fail")
	}
}

func TestPasswordHashesUseDifferentSalts(t *testing.T) {
	first, err := HashPassword("same password")
	if err != nil {
		t.Fatalf("HashPassword first: %v", err)
	}
	second, err := HashPassword("same password")
	if err != nil {
		t.Fatalf("HashPassword second: %v", err)
	}
	if first == second {
		t.Fatal("expected different salts")
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	for _, encoded := range []string{"", "plain", "$argon2id$v=19$m=19456,t=2,p=1$bad"} {
		if _, err := VerifyPassword(encoded, "password"); err == nil {
			t.Fatalf("expected malformed hash error for %q", encoded)
		}
	}
}

func TestRandomTokenAndHash(t *testing.T) {
	first, err := RandomToken(32)
	if err != nil {
		t.Fatalf("RandomToken: %v", err)
	}
	second, err := RandomToken(32)
	if err != nil {
		t.Fatalf("RandomToken: %v", err)
	}
	if first == second {
		t.Fatal("tokens should be unique")
	}
	if first == "" || HashToken(first) == first {
		t.Fatal("token should be hashed")
	}
	if got := HashToken("token"); got != "3c469e9d6c5875d37a43f353d4f88e61fcf812c66eee3457465a40b0da4153e0" {
		t.Fatalf("token hash = %q", got)
	}
	if _, err := RandomToken(0); err == nil {
		t.Fatal("expected zero-length token error")
	}
}
