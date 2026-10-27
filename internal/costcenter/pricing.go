package costcenter

import (
	"sort"
	"unicode/utf8"
)

// Model rates: public rates ship with the binary; local rates stay in the local database.

func validateRate(value any) ([]string, error) {
	body, ok := value.(map[string]any)
	if !ok {
		return nil, invalid("Rate must be a JSON object")
	}
	result := make([]string, len(rateFields))
	for i, key := range rateFields {
		d, err := ParseDecimal(pyStr(body[key]))
		if err != nil {
			return nil, invalid("Rates must be numbers")
		}
		if !d.Finite || d.Value.Sign() < 0 {
			return nil, invalid("Rates must be finite and nonnegative")
		}
		result[i] = d.Text
	}
	// A price per image is optional, for image models.
	perImage := "0"
	if value, ok := body["per_image"]; ok && value != nil && pyStr(value) != "" {
		d, err := ParseDecimal(pyStr(value))
		if err != nil {
			return nil, invalid("Rates must be numbers")
		}
		if !d.Finite || d.Value.Sign() < 0 {
			return nil, invalid("Rates must be finite and nonnegative")
		}
		perImage = d.Text
	}
	return append(result, perImage), nil
}

func (s *Store) SetLocalRate(model string, body any) error {
	if model == "" || utf8.RuneCountInString(model) > 200 {
		return invalid("Model must be a name of at most 200 characters")
	}
	rate, err := validateRate(body)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec("INSERT OR REPLACE INTO rates (model, input_per_million, cached_input_per_million, output_per_million, updated_at, per_image) "+
		"VALUES (?, ?, ?, ?, ?, ?)", model, rate[0], rate[1], rate[2], nowISO(), rate[3])
	return err
}

func (s *Store) DeleteLocalRate(model string) error {
	result, err := s.DB.Exec("DELETE FROM rates WHERE model=?", model)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return &NotFound{"No local rate for " + model}
	}
	return nil
}

// ModelPricing is one model seen in usage, with its rate source and all-time estimated cost.
type ModelPricing struct {
	Model string `json:"model"`
	Totals
	Source       string      `json:"source"`
	Rate         *RateValues `json:"rate"`
	PublicRate   *RateValues `json:"public_rate"`
	VerifiedFrom *string     `json:"verified_from"`
	SourceURL    *string     `json:"source_url"`
	TokenShare   float64     `json:"token_share"`
}

func (s *Store) PricingTable(rates []Rate) ([]ModelPricing, error) {
	rows, err := s.Query(Filters{Period: "all"})
	if err != nil {
		return nil, err
	}
	order, byModel := bucket(rows, func(r Row) string { return r.Model })
	var totalTokens int64
	for _, r := range rows {
		totalTokens += r.Input + r.Output
	}
	result := []ModelPricing{}
	for _, model := range order {
		items := byModel[model]
		t := totals(items, rates)
		var public []Rate
		local := false
		for _, r := range rates {
			if r.Model == model {
				if r.Local {
					local = true
				} else {
					public = append(public, r)
				}
			}
		}
		day := items[len(items)-1].RecordedAt
		if len(day) > 10 {
			day = day[:10]
		}
		m := ModelPricing{Model: model, Totals: t, Source: "missing"}
		if rate, _ := rateFor(model, day, rates); rate != nil {
			v := rate.Values()
			m.Rate = &v
		}
		if len(public) > 0 {
			m.Source = "public"
			last := public[len(public)-1]
			v := last.Values()
			m.PublicRate = &v
			verified := public[0].EffectiveFrom
			for _, r := range public {
				if r.EffectiveFrom < verified {
					verified = r.EffectiveFrom
				}
			}
			m.VerifiedFrom = &verified
			m.SourceURL = optional(last.SourceURL)
		}
		if local {
			m.Source = "local"
		}
		if totalTokens > 0 {
			m.TokenShare = float64(t.tokens()) / float64(totalTokens) * 100
		}
		result = append(result, m)
	}
	rank := map[string]int{"missing": 0, "local": 1, "public": 2}
	sort.SliceStable(result, func(i, j int) bool {
		if rank[result[i].Source] != rank[result[j].Source] {
			return rank[result[i].Source] < rank[result[j].Source]
		}
		return result[i].tokens() > result[j].tokens()
	})
	return result, nil
}
