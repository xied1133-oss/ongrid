package tenantctx

import (
	"context"
	"testing"
)

func TestWithAndFrom(t *testing.T) {
	ctx := context.Background()
	if _, ok := From(ctx); ok {
		t.Fatal("From on bare ctx should be ok=false")
	}

	want := Tenant{UserID: 42, Role: "admin"}
	ctx = With(ctx, want)

	got, ok := From(ctx)
	if !ok {
		t.Fatal("From after With should be ok=true")
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
