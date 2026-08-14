package marketindex

import (
	"testing"
	"time"
)

func quote(sym, name string, price float64) Quote {
	return Quote{Symbol: sym, Name: name, Price: price, FetchedAt: time.Now()}
}

func quote10d(sym, name string, price, chg10d float64) Quote {
	q := quote(sym, name, price)
	q.Change10d = chg10d
	return q
}

func TestDivergenceFollowsFall(t *testing.T) {
	// S&P caiu -3% (10d) e o cripto só -0.5% → gap de -2.5 → seguir a QUEDA.
	snap := Snapshot{
		Regime: "risk-off",
		Quotes: []Quote{
			quote10d("^GSPC", "S&P 500", 7000, -3.0),
			quote("^VIX", "VIX", 28),
		},
	}
	sig := AnalyzeDivergence(snap, -0.5, DefaultDivergenceConfig())
	if sig.Action != "sell" {
		t.Fatalf("esperava sell (S&P caiu, cripto não reagiu), got %s — %s", sig.Action, sig.Reason)
	}
	if sig.Strength <= 0 {
		t.Fatalf("strength deveria ser positiva, got %v", sig.Strength)
	}
}

func TestDivergenceFollowsRise(t *testing.T) {
	// S&P subiu +3% e o cripto só +0.5% → gap +2.5 → seguir a ALTA.
	snap := Snapshot{
		Regime: "risk-on",
		Quotes: []Quote{
			quote10d("^GSPC", "S&P 500", 7800, 3.0),
			quote("^VIX", "VIX", 14),
		},
	}
	sig := AnalyzeDivergence(snap, 0.5, DefaultDivergenceConfig())
	if sig.Action != "buy" {
		t.Fatalf("esperava buy (S&P subiu, cripto atrás), got %s — %s", sig.Action, sig.Reason)
	}
}

func TestDivergenceNoEvent(t *testing.T) {
	// Sem movimento global relevante → hold.
	snap := Snapshot{
		Regime: "risk-on",
		Quotes: []Quote{quote10d("^GSPC", "S&P 500", 7800, 0.3)},
	}
	sig := AnalyzeDivergence(snap, 0.4, DefaultDivergenceConfig())
	if sig.Action != "hold" {
		t.Fatalf("esperava hold (sem evento), got %s", sig.Action)
	}
}

func TestDivergenceNoData(t *testing.T) {
	// Sem S&P no snapshot → hold com motivo claro (proteção por falta de dados).
	snap := Snapshot{Quotes: []Quote{quote("^VIX", "VIX", 14)}}
	sig := AnalyzeDivergence(snap, 0.0, DefaultDivergenceConfig())
	if sig.Action != "hold" || sig.Reason == "" {
		t.Fatalf("sem S&P deveria ser hold com motivo, got %s %q", sig.Action, sig.Reason)
	}
}

func TestDivergenceRiskOffBoost(t *testing.T) {
	// Risk-off + queda global = confiança alta → strength próxima de 1.
	snap := Snapshot{
		Regime: "risk-off",
		Quotes: []Quote{quote10d("^GSPC", "S&P 500", 7000, -4.0)},
	}
	sig := AnalyzeDivergence(snap, 0.0, DefaultDivergenceConfig())
	if sig.Strength < 0.5 {
		t.Fatalf("risk-off deveria reforçar a confiança, strength=%v", sig.Strength)
	}
}
