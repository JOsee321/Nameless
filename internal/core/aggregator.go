// Package core provides the unified result schema and deduplication logic
// used by all scan modules and the correlation layer.
package core

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"time"
)

// EntityType classifies the kind of data an Entity represents.
type EntityType string

const (
	EntityDomain    EntityType = "Domain"
	EntitySubdomain EntityType = "Subdomain"
	EntityEmail     EntityType = "Email"
	EntityUsername  EntityType = "Username"
	EntityPlatform  EntityType = "Platform"
	EntitySecret    EntityType = "Secret"
	EntityEndpoint  EntityType = "Endpoint"
)

// Entity is the universal result record written by every module.
// All fields correspond directly to the schema in PRD §5.4.
type Entity struct {
	// ID is a stable, content-derived identifier so duplicate discoveries
	// from different modules do not create duplicate entries.
	ID string `json:"id"`

	Type  EntityType `json:"type"`
	Value string     `json:"value"`

	// SourceModule is the Name() of the module that discovered this entity.
	SourceModule string `json:"source_module"`

	// RelatedTo holds IDs of other entities this one is connected to,
	// forming the edges of the result graph used by the correlator.
	RelatedTo []string `json:"related_to,omitempty"`

	// Metadata carries module-specific key/value details (e.g. HTTP status,
	// platform URL, matched regex) without bloating the core schema.
	Metadata map[string]string `json:"metadata,omitempty"`

	Timestamp time.Time `json:"timestamp"`
}

// NewEntity creates an Entity and derives its ID from type+value so that
// the same discovery from two different modules produces the same ID.
func NewEntity(t EntityType, value, sourceModule string) Entity {
	return Entity{
		ID:           entityID(t, value),
		Type:         t,
		Value:        value,
		SourceModule: sourceModule,
		Timestamp:    time.Now().UTC(),
	}
}

// entityID produces a short, deterministic identifier from type and value.
func entityID(t EntityType, value string) string {
	h := sha256.Sum256([]byte(string(t) + ":" + value))
	return fmt.Sprintf("%x", h[:8]) // 16 hex chars — short but collision-resistant for OSINT scale
}

// Aggregator collects Entity values from all modules, deduplicates them by ID,
// and provides thread-safe access for the correlator and output layer.
type Aggregator struct {
	mu       sync.RWMutex
	entities map[string]Entity // keyed by Entity.ID
}

// NewAggregator returns an empty, ready-to-use Aggregator.
func NewAggregator() *Aggregator {
	return &Aggregator{entities: make(map[string]Entity)}
}

// Add inserts e into the aggregator.  If an entity with the same ID already
// exists, Add is a no-op (first-write-wins, preserving the original source).
func (a *Aggregator) Add(e Entity) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, exists := a.entities[e.ID]; !exists {
		a.entities[e.ID] = e
	}
}

// All returns a snapshot of every unique entity collected so far.
func (a *Aggregator) All() []Entity {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]Entity, 0, len(a.entities))
	for _, e := range a.entities {
		out = append(out, e)
	}
	return out
}

// Len returns the number of unique entities stored.
func (a *Aggregator) Len() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.entities)
}

// ByType returns all entities of the given type. The returned slice is a
// snapshot; mutations to it do not affect the Aggregator.
func (a *Aggregator) ByType(t EntityType) []Entity {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var out []Entity
	for _, e := range a.entities {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

// Has reports whether an entity with the given type and value has been recorded.
func (a *Aggregator) Has(t EntityType, value string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	_, ok := a.entities[entityID(t, value)]
	return ok
}
