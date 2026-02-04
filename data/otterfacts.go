package data

import (
	_ "embed"
	"encoding/json"
	"errors"
	"math/rand"
	"sync"
	"time"
)

type OtterFact struct {
	ID       int    `json:"id"`
	Fact     string `json:"fact"`
	Category string `json:"category"`
}

//go:embed otter-facts.json
var otterFactsJSON []byte

var (
	otterFactsOnce sync.Once
	otterFacts     []OtterFact
	otterFactsErr  error

	rng   = rand.New(rand.NewSource(time.Now().UnixNano()))
	rngMu sync.Mutex
)

func loadOtterFacts() ([]OtterFact, error) {
	otterFactsOnce.Do(func() {
		if len(otterFactsJSON) == 0 {
			otterFactsErr = errors.New("otter facts JSON is empty")
			return
		}

		if err := json.Unmarshal(otterFactsJSON, &otterFacts); err != nil {
			otterFactsErr = err
			return
		}

		if len(otterFacts) == 0 {
			otterFactsErr = errors.New("otter facts JSON contains no entries")
		}
	})

	return otterFacts, otterFactsErr
}

func randomIndex(max int) int {
	rngMu.Lock()
	defer rngMu.Unlock()
	return rng.Intn(max)
}

func RandomOtterFact() (OtterFact, error) {
	facts, err := loadOtterFacts()
	if err != nil {
		return OtterFact{}, err
	}

	return facts[randomIndex(len(facts))], nil
}
