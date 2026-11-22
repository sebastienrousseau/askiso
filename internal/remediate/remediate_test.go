// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package remediate

import "testing"

func TestAddressRemediation(t *testing.T) {
	if res := Address(nil, "", ""); res.Strategy != "empty" || res.Confidence != 0 {
		t.Fatalf("empty = %+v", res)
	}

	// Structured address with postcode, city, country, and leading building number.
	res := Address([]string{"10 Downing Street", "SW1A 2AA London, GB"}, "", ModeStructured)
	if res.Address.Country != "GB" || res.Address.PostCode != "SW1A 2AA" || res.Address.TownName != "London" || res.Address.BuildingNumber != "10" || res.Address.StreetName != "Downing Street" {
		t.Fatalf("structured GB = %+v", res)
	}
	if res.Confidence < 0.9 {
		t.Errorf("expected high confidence, got %v", res.Confidence)
	}

	// Hybrid address with trailing building number and standalone country line.
	res2 := Address([]string{"Bahnhofstrasse 42", "8001 Zurich", "CH"}, "", ModeHybrid)
	if res2.Address.Country != "CH" || res2.Address.PostCode != "8001" || res2.Address.TownName != "Zurich" {
		t.Fatalf("hybrid CH = %+v", res2)
	}

	// Trailing building number in structured mode.
	res3 := Address([]string{"Bahnhofstrasse 42", "8001 Zurich"}, "CH", ModeStructured)
	if res3.Address.StreetName != "Bahnhofstrasse" || res3.Address.BuildingNumber != "42" {
		t.Fatalf("structured CH = %+v", res3)
	}

	// Street without building number + fallback town.
	res4 := Address([]string{"Rue de Rivoli", "Paris"}, "FR", ModeStructured)
	if res4.Address.StreetName != "Rue de Rivoli" || res4.Address.TownName != "Paris" || res4.Strategy != "deterministic-country-city" {
		t.Fatalf("fallback town FR = %+v", res4)
	}

	// Partial & unremediated.
	if res5 := Address([]string{"Unknown Street"}, "DE", ModeHybrid); res5.Strategy != "partial" {
		t.Fatalf("partial = %+v", res5)
	}
	if res6 := Address([]string{"Just one line"}, "", ModeHybrid); res6.Strategy != "unremediated" {
		t.Fatalf("unremediated = %+v", res6)
	}
}
