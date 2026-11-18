// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

// Package remediate provides deterministic, offline-first remediation of
// unstructured ISO 20022 postal addresses (<AdrLine>) into hybrid or
// structured <PstlAdr> elements for SWIFT SR2027 readiness.
//
// It never transmits payment data or PII to external services and attaches an
// auditable confidence score and strategy label to every suggestion.
package remediate

import (
	"regexp"
	"strings"
)

// TargetMode selects the output postal-address structure.
type TargetMode string

const (
	// ModeHybrid emits <TwnNm>, <Ctry>, and remaining <AdrLine> entries.
	ModeHybrid TargetMode = "hybrid"
	// ModeStructured emits <StrtNm>, <BldgNb>, <PstCd>, <TwnNm>, and <Ctry>.
	ModeStructured TargetMode = "structured"
)

// StructuredAddress holds parsed ISO 20022 <PstlAdr> components.
type StructuredAddress struct {
	StreetName     string   `json:"street_name,omitempty"`
	BuildingNumber string   `json:"building_number,omitempty"`
	PostCode       string   `json:"post_code,omitempty"`
	TownName       string   `json:"town_name,omitempty"`
	Country        string   `json:"country,omitempty"`
	RemainingLines []string `json:"remaining_lines,omitempty"`
}

// Result records a single deterministic address remediation outcome.
type Result struct {
	Mode       TargetMode        `json:"mode"`
	Address    StructuredAddress `json:"address"`
	Confidence float64           `json:"confidence"`
	Strategy   string            `json:"strategy"`
}

var (
	countryTokenRE = regexp.MustCompile(`^[A-Z]{2}$`)
	postCodeCityRE = regexp.MustCompile(`^([A-Z0-9][A-Z0-9\- ]{1,9}?)\s+([A-Za-z][A-Za-z\s\-']{1,40})$`)
	leadingNumRE   = regexp.MustCompile(`^([0-9]{1,6}[A-Za-z]?)\s+(.+)$`)
	trailingNumRE  = regexp.MustCompile(`^(.+?)\s+([0-9]{1,6}[A-Za-z]?)$`)
)

// Address converts unstructured <AdrLine> values (with an optional known ISO
// 3166-1 alpha-2 country code) into a hybrid or structured address candidate.
func Address(lines []string, knownCountry string, mode TargetMode) Result {
	if mode == "" {
		mode = ModeHybrid
	}
	cleaned := make([]string, 0, len(lines))
	for _, l := range lines {
		if s := strings.TrimSpace(l); s != "" {
			cleaned = append(cleaned, s)
		}
	}
	if len(cleaned) == 0 {
		return Result{Mode: mode, Confidence: 0, Strategy: "empty"}
	}

	addr := StructuredAddress{Country: strings.ToUpper(strings.TrimSpace(knownCountry))}
	remaining := make([]string, 0, len(cleaned))

	for i, line := range cleaned {
		// Check trailing ", CC" or standalone "CC" on the last line.
		if i == len(cleaned)-1 && addr.Country == "" {
			if countryTokenRE.MatchString(line) {
				addr.Country = line
				continue
			}
			if idx := strings.LastIndex(line, ","); idx >= 0 {
				tail := strings.TrimSpace(line[idx+1:])
				if countryTokenRE.MatchString(tail) {
					addr.Country = tail
					line = strings.TrimSpace(line[:idx])
				}
			}
		}

		// Try "POSTCODE TOWN" on later lines.
		if i > 0 && addr.TownName == "" {
			if m := postCodeCityRE.FindStringSubmatch(line); m != nil {
				addr.PostCode = strings.TrimSpace(m[1])
				addr.TownName = strings.TrimSpace(m[2])
				continue
			}
		}
		remaining = append(remaining, line)
	}

	if addr.TownName == "" && len(remaining) > 1 {
		addr.TownName = remaining[len(remaining)-1]
		remaining = remaining[:len(remaining)-1]
	}

	if mode == ModeStructured && len(remaining) > 0 {
		street := remaining[0]
		if m := leadingNumRE.FindStringSubmatch(street); m != nil {
			addr.BuildingNumber = m[1]
			addr.StreetName = m[2]
			remaining = remaining[1:]
		} else if m := trailingNumRE.FindStringSubmatch(street); m != nil {
			addr.StreetName = m[1]
			addr.BuildingNumber = m[2]
			remaining = remaining[1:]
		} else {
			addr.StreetName = street
			remaining = remaining[1:]
		}
	}

	addr.RemainingLines = remaining

	confidence, strategy := score(addr, mode)
	return Result{
		Mode:       mode,
		Address:    addr,
		Confidence: confidence,
		Strategy:   strategy,
	}
}

func score(addr StructuredAddress, mode TargetMode) (float64, string) {
	if addr.Country != "" && addr.TownName != "" && addr.PostCode != "" {
		if mode == ModeStructured && addr.StreetName != "" {
			return 0.95, "deterministic-postcode-city-street"
		}
		return 0.90, "deterministic-postcode-city"
	}
	if addr.Country != "" && addr.TownName != "" {
		return 0.75, "deterministic-country-city"
	}
	if addr.TownName != "" || addr.Country != "" {
		return 0.50, "partial"
	}
	return 0.10, "unremediated"
}
