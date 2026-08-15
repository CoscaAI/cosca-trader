// errors.go — hierarquia de erros da camada de exchange (padrão ccxt):
// separa erros RETRYABLE (rede/timeout/rate limit) de erros DETERMINÍSTICOS
// (ordem inválida, saldo insuficiente, ordem não encontrada). É o que permite
// ao OMS/saga decidir entre re-tentar (backoff) e falhar rápido — sem
// type-switch por exchange.
package exchange

import "errors"

// ErrorKind classifica um erro de exchange para a decisão de retry.
type ErrorKind int

const (
	// KindDeterministic é um erro que NÃO muda com retry: ordem inválida,
	// saldo insuficiente, ordem não encontrada, símbolo inválido. Falha rápido.
	KindDeterministic ErrorKind = iota
	// KindRateLimit é limite de taxa (HTTP 429/418). Retry com backoff.
	KindRateLimit
	// KindTransient é rede/timeout/5xx/serviço indisponível. Retry com backoff.
	KindTransient
)

// Error embrulha um erro de exchange com a sua classificação.
type Error struct {
	Kind ErrorKind
	Err  error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// Wrap classifica um erro de exchange. nil devolve nil.
func Wrap(kind ErrorKind, err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*Error); ok {
		return err // já classificado — não re-embrulha
	}
	return &Error{Kind: kind, Err: err}
}

// KindOf devolve a classificação de um erro (KindDeterministic se não
// classificado — o default seguro é NÃO retryable).
func KindOf(err error) ErrorKind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindDeterministic
}

// IsRetryable devolve true se o erro deve ser re-tentado (rate limit ou
// transitório de rede). Erros determinísticos e erros não classificados
// devolvem false (fail-closed: na dúvida, não martela a exchange).
func IsRetryable(err error) bool {
	k := KindOf(err)
	return k == KindRateLimit || k == KindTransient
}
