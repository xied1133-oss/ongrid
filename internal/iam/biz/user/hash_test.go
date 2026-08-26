package user

import "testing"

func TestHashAndVerifyRoundTrip(t *testing.T) {
	enc, err := hashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	if !verifyPassword("correct-horse-battery-staple", enc) {
		t.Fatal("verifyPassword: want true for matching password")
	}
	if verifyPassword("wrong", enc) {
		t.Fatal("verifyPassword: want false for mismatching password")
	}
}

func TestVerifyPasswordRejectsGarbage(t *testing.T) {
	cases := []string{
		"",
		"notargon",
		"$argon2id$v=99$m=1,t=1,p=1$aaaa$bbbb", // wrong version
		"$argon2id$v=19$bad-params$aaaa$bbbb",
	}
	for _, c := range cases {
		if verifyPassword("anything", c) {
			t.Errorf("verifyPassword(%q) returned true; want false", c)
		}
	}
}
