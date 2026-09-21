package web

import "testing"

// Pętla odpowiedzi dowoziła 21 września 2026 trzy odpowiedzi z trzynastu,
// a od czerwca nie odebrał nikt. Tego dnia powtórne pytanie stało się drugą
// drogą odbioru; te liczby na pulpicie są jedynym sposobem sprawdzenia, czy
// zadziałała — i czy to ona, a nie fetch_answer, wykonuje robotę.
func TestDeliveryRate(t *testing.T) {
	cases := []struct {
		name               string
		delivered, written int
		want               int
	}{
		{"wartość odniesienia z 21 IX", 3, 13, 23},
		{"nic nie napisane — zero, nie dzielenie przez zero", 0, 0, 0},
		{"nic nie odebrane", 0, 12, 0},
		{"wszystko odebrane", 7, 7, 100},
		{"ujemne wejście nie wybucha", 1, -3, 0},
	}
	for _, c := range cases {
		if got := deliveryRate(c.delivered, c.written); got != c.want {
			t.Errorf("%s: deliveryRate(%d, %d) = %d, oczekiwano %d",
				c.name, c.delivered, c.written, got, c.want)
		}
	}
}

// „100% z niczego" byłoby najgorszym możliwym odczytem: pusty pulpit
// wyglądałby jak pętla działająca bez zarzutu.
func TestDeliveryRateNeverClaimsSuccessOnEmpty(t *testing.T) {
	if got := deliveryRate(0, 0); got == 100 {
		t.Error("pusty stan raportuje 100% — pulpit kłamałby na starcie")
	}
}

func TestReAskPickupRecognised(t *testing.T) {
	if !isReAskPickup("agent (re-ask)") {
		t.Error("odbiór przez powtórne pytanie nierozpoznany")
	}
	if isReAskPickup("agent") {
		t.Error("zwykły fetch_answer policzony jako powtórka")
	}
	if isReAskPickup("") {
		t.Error("puste FetchedBy policzone jako powtórka")
	}
}
