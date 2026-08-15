package exchange

import (
	"errors"
	"testing"
)

func TestWrapAndKindOf(t *testing.T) {
	err := Wrap(KindRateLimit, errors.New("429"))
	if KindOf(err) != KindRateLimit {
		t.Fatalf("KindOf = %v, esperava KindRateLimit", KindOf(err))
	}
	// nil → nil
	if Wrap(KindTransient, nil) != nil {
		t.Fatal("Wrap(nil) deveria devolver nil")
	}
	// já classificado não é re-embrulhado
	inner := Wrap(KindRateLimit, errors.New("x"))
	outer := Wrap(KindTransient, inner)
	if KindOf(outer) != KindRateLimit {
		t.Fatalf("re-wrap mudou a classe: %v", KindOf(outer))
	}
}

func TestIsRetryable(t *testing.T) {
	if !IsRetryable(Wrap(KindRateLimit, errors.New("429"))) {
		t.Fatal("rate limit deveria ser retryable")
	}
	if !IsRetryable(Wrap(KindTransient, errors.New("timeout"))) {
		t.Fatal("transitório deveria ser retryable")
	}
	if IsRetryable(Wrap(KindDeterministic, errors.New("ordem inválida"))) {
		t.Fatal("determinístico não deveria ser retryable")
	}
	// erro não classificado → fail-closed (não retryable)
	if IsRetryable(errors.New("desconhecido")) {
		t.Fatal("erro não classificado deveria ser fail-closed (não retryable)")
	}
}

func TestErrorUnwrap(t *testing.T) {
	base := errors.New("base")
	wrapped := Wrap(KindTransient, base)
	if !errors.Is(wrapped, base) {
		t.Fatal("errors.Is deveria alcançar o erro base")
	}
}
