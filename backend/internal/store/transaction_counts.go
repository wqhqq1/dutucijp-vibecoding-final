package store

import (
	"strings"
	"sync"
)

// Published input is immutable. This bounded, process-local cache stores exact
// counts only, never rows, account information, workflow state or permissions.
// Every caller must still validate the published run and current authorization.
const transactionCountCapacity = 128

type transactionCountKey struct {
	dataset, customer, stock, invoice, country, kind, from, to string
}

type transactionCountCache struct {
	mu     sync.Mutex
	values map[transactionCountKey]int
	keys   []transactionCountKey
	next   int
}

func immutableCountKey(f QueryFilter) (transactionCountKey, bool) {
	key := transactionCountKey{f.datasetID, f.Customer, f.Stock, f.Invoice, f.Country, f.Kind, f.DateFrom, f.DateTo}
	// Bound retained key memory too, including arbitrarily long client filters.
	bytes := len(key.dataset) + len(key.customer) + len(key.stock) + len(key.invoice) + len(key.country) + len(key.kind) + len(key.from) + len(key.to)
	return key, bytes <= 4096
}

func (c *transactionCountCache) get(key transactionCountKey) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.values[key]
	return value, ok
}

func (c *transactionCountCache) put(key transactionCountKey, count int) {
	// URL values may be short subslices of a much larger RawQuery. Own only the
	// bounded key bytes instead of retaining that entire request buffer.
	key = transactionCountKey{
		strings.Clone(key.dataset), strings.Clone(key.customer), strings.Clone(key.stock), strings.Clone(key.invoice),
		strings.Clone(key.country), strings.Clone(key.kind), strings.Clone(key.from), strings.Clone(key.to),
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.values == nil {
		c.values = make(map[transactionCountKey]int)
	}
	if _, exists := c.values[key]; !exists {
		if len(c.keys) < transactionCountCapacity {
			c.keys = append(c.keys, key)
		} else {
			delete(c.values, c.keys[c.next])
			c.keys[c.next] = key
			c.next = (c.next + 1) % transactionCountCapacity
		}
	}
	c.values[key] = count
}
