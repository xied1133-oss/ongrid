package passwd

import "testing"

func TestHashVerifyRoundTrip(t *testing.T) {
	enc, err := Hash("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !Verify("correct-horse-battery-staple", enc) {
		t.Fatal("Verify: want true for matching plaintext")
	}
	if Verify("wrong", enc) {
		t.Fatal("Verify: want false for mismatching plaintext")
	}
}

func TestHashRejectsEmpty(t *testing.T) {
	if _, err := Hash(""); err == nil {
		t.Fatal("Hash(\"\"): want error")
	}
}

func TestVerifyRejectsGarbage(t *testing.T) {
	cases := []string{
		"",
		"notargon",
		"$argon2id$v=99$m=1,t=1,p=1$aaaa$bbbb", // wrong version
		"$argon2id$v=19$bad-params$aaaa$bbbb",
	}
	for _, c := range cases {
		if Verify("anything", c) {
			t.Errorf("Verify(%q) returned true; want false", c)
		}
	}
}

func TestHashProducesDifferentOutputsForSameInput(t *testing.T) {
	a, err := Hash("same-input")
	if err != nil {
		t.Fatalf("Hash a: %v", err)
	}
	b, err := Hash("same-input")
	if err != nil {
		t.Fatalf("Hash b: %v", err)
	}
	if a == b {
		t.Fatal("two Hash calls of same input produced identical output; salt is not random")
	}
}
