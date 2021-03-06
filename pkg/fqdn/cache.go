// Copyright 2018 Authors of Cilium
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package fqdn

import (
	"encoding/json"
	"net"
	"regexp"
	"sort"
	"time"
	"unsafe"

	"github.com/cilium/cilium/pkg/ip"
	"github.com/cilium/cilium/pkg/lock"
)

// DefaultDNSCache is a global, shared, DNS cache. It is the default cache used
// by DNSPoller instances, unless initialized to use another.
// Note: The DNSCache type returns all DNS information, regardless of source.
var DefaultDNSCache = NewDNSCache()

// cacheEntry objects hold data passed in via DNSCache.Update, nominally
// equating to a DNS lookup. They are internal to DNSCache and should not be
// returned.
// cacheEntry objects are immutable once created; the address of an instance is
// a unique identifier.
type cacheEntry struct {
	// Name is a DNS name, it my be not fully qualified (e.g. myservice.namespace)
	Name string `json:"name,omitempty"`

	// LookupTime is when the data begins being valid
	LookupTime time.Time `json:"lookuptime,omitempty"`

	// ExpirationTime is a calcutated time when the DNS data stops being valid.
	// It is simply LookupTime + TTL
	ExpirationTime time.Time `json:"expirationtime,omitempty"`

	// TTL represents the number of seconds past LookupTime that this data is
	// valid.
	TTL int `json:"ttl,omitempty"`

	// IPs are the IPs associated with Name for this cacheEntry.
	IPs []net.IP `json:"ips,omitempty"`
}

// isExpiredBy returns true if entry is no longer valid at pointInTime
func (entry *cacheEntry) isExpiredBy(pointInTime time.Time) bool {
	return pointInTime.After(entry.ExpirationTime)
}

// ipEntries maps a unique IP to the cacheEntry that provides it in .IPs.
// Multiple IPs may point to the same cacheEntry, or they may all be different.
// Crucially, an IP may be present in a cacheEntry but the IP in ipEntries
// points to another cacheEntry. This is because the second cacheEntry has a
// later expiration for this specific IP, and may not include the other IPs
// provided by the first entry.
// The DNS name in the entries is not checked, but is assumed to be the same
// for all entries.
// Note: They are guarded by the DNSCache mutex.
type ipEntries map[string]*cacheEntry

// nameEntries maps a DNS name to the cache entry that inserted it into the
// cache. It used in reverse DNS lookups. It is similar to ipEntries, above,
// but the key is a DNS name.
type nameEntries map[string]*cacheEntry

// getIPs returns a sorted list of non-expired unique IPs.
// This needs a read-lock
func (s ipEntries) getIPs(now time.Time) []net.IP {
	ips := make([]net.IP, 0, len(s)) // worst case size
	for ip, entry := range s {
		if entry != nil && !entry.isExpiredBy(now) {
			ips = append(ips, net.ParseIP(ip))
		}
	}

	return ip.KeepUniqueIPs(ips) // sorts IPs
}

// DNSCache manages DNS data that will expire after a certain TTL. Information
// is tracked per-IP address, retaining the latest-expiring DNS data for each
// address.
// For most real-world DNS data, the entry per name remains small because newer
// lookups replace older ones. Large TTLs may cause entries to grow if many
// unique IPs are returned in separate lookups.
// Redundant or expired entries are removed on insert.
// Lookups check for expired entries.
type DNSCache struct {
	lock.RWMutex

	// forward DNS lookups name -> IPEntries
	// IPEntries maps IP -> entry that provides it. An entry may provide multiple IPs.
	forward map[string]ipEntries

	// IP->dnsNames lookup
	// This map is subordinate to forward, above. An IP inserted into forward, or
	// expired in forward, should also be added/removed in reverse.
	reverse map[string]nameEntries
}

// NewDNSCache returns an initialized DNSCache
func NewDNSCache() *DNSCache {
	c := &DNSCache{
		forward: make(map[string]ipEntries),
		reverse: make(map[string]nameEntries),
	}

	return c
}

// Update inserts a new entry into the cache.
// After insertion cache entries for name are expired and redundant entries
// evicted. This is O(number of new IPs) for eviction, and O(number of IPs for
// name) for expiration.
// lookupTime is the time the DNS information began being valid. It should be
// in the past.
// name is used as is and may be an unqualified name (e.g. myservice.namespace).
// ips may be an IPv4 or IPv6 IP. Duplicates will be removed.
// ttl is the DNS TTL for ips and is a seconds value.
func (c *DNSCache) Update(lookupTime time.Time, name string, ips []net.IP, ttl int) {
	entry := &cacheEntry{
		Name:           name,
		LookupTime:     lookupTime,
		ExpirationTime: lookupTime.Add(time.Duration(ttl) * time.Second),
		TTL:            ttl,
		IPs:            ips,
	}

	c.Lock()
	defer c.Unlock()

	c.updateWithEntry(entry)
}

// updateWithEntry implements the insertion of a cacheEntry. It is used by
// DNSCache.Update and DNSCache.UpdateWithEntry.
// This needs a write lock
func (c *DNSCache) updateWithEntry(entry *cacheEntry) {
	entries, exists := c.forward[entry.Name]
	if !exists {
		entries = make(map[string]*cacheEntry)
		c.forward[entry.Name] = entries
	}
	c.updateWithEntryIPs(entries, entry)
	// When lookupTime is much earlier than time.Now(), we may not expire all
	// entries that should be expired, leaving more work for .Lookup.
	c.removeExpired(entries, time.Now())
}

// UpdateFromCache is a utility function that allows updating a DNSCache
// instance with all the internal entries of another. Latest-Expiration still
// applies, thus the merged outcome is consistent with adding the entries
// individually.
func (c *DNSCache) UpdateFromCache(update *DNSCache) {
	if update == nil {
		return
	}

	update.RLock()
	defer update.RUnlock()

	c.Lock()
	defer c.Unlock()

	for _, newEntries := range update.forward {
		for _, newEntry := range newEntries {
			c.updateWithEntry(newEntry)
		}
	}
}

// Lookup returns a set of unique IPs that are currently unexpired for name, if
// any exist. An empty list indicates no valid records exist. The IPs are
// returned sorted.
func (c *DNSCache) Lookup(name string) (ips []net.IP) {
	c.RLock()
	defer c.RUnlock()

	return c.lookupByTime(time.Now(), name)
}

// lookupByTime takes a timestamp for expiration comparisions, and is only
// intended for testing.
func (c *DNSCache) lookupByTime(now time.Time, name string) (ips []net.IP) {
	entries, found := c.forward[name]
	if !found {
		return nil
	}

	return entries.getIPs(now)
}

// LookupByRegexp returns all non-expired cache entries that match re as a map
// of name -> IPs
func (c *DNSCache) LookupByRegexp(re *regexp.Regexp) (matches map[string][]net.IP) {
	return c.lookupByRegexpByTime(time.Now(), re)
}

// lookupByRegexpByTime takes a timestamp for expiration comparisions, and is
// only intended for testing.
func (c *DNSCache) lookupByRegexpByTime(now time.Time, re *regexp.Regexp) (matches map[string][]net.IP) {
	matches = make(map[string][]net.IP)

	c.RLock()
	defer c.RUnlock()

	for name, entry := range c.forward {
		if re.MatchString(name) {
			matches[name] = append(matches[name], entry.getIPs(now)...)
		}
	}

	return matches
}

// LookupIP returns all DNS names in entries that include that IP. The cache
// maintains the latest-expiring entry per-name per-IP. This means that multiple
// names referrring to the same IP will expire from the cache at different times,
// and only 1 entry for each name-IP pair is internally retained.
func (c *DNSCache) LookupIP(ip net.IP) (names []string) {
	c.RLock()
	defer c.RUnlock()

	return c.lookupIPByTime(time.Now(), ip)
}

// lookupIPByTime takes a timestamp for expiration comparisions, and is
// only intended for testing.
func (c *DNSCache) lookupIPByTime(now time.Time, ip net.IP) (names []string) {
	ipKey := ip.String()
	cacheEntries, found := c.reverse[ipKey]
	if !found {
		return nil
	}

	for name, entry := range cacheEntries {
		if entry != nil && !entry.isExpiredBy(now) {
			names = append(names, name)
		}
	}

	sort.Strings(names)
	return names
}

// updateWithEntry adds a mapping for every IP found in `entry` to `ipEntries`
// (which maps IP -> cacheEntry). It will replace existing IP->old mappings in
// `entries` if the current entry expires sooner (or has already expired).
// This needs a write lock
func (c *DNSCache) updateWithEntryIPs(entries ipEntries, entry *cacheEntry) {
	for _, ip := range entry.IPs {
		ipStr := ip.String()
		old, exists := entries[ipStr]
		if old == nil || !exists || old.isExpiredBy(entry.ExpirationTime) {
			entries[ipStr] = entry
			c.upsertReverse(ipStr, entry)
		}
	}
}

// removeExpired removes expired (or nil) cacheEntry pointers from entries, an
// ipEntries for a specific name.
// This needs a write lock
func (c *DNSCache) removeExpired(entries ipEntries, now time.Time) {
	for ip, entry := range entries {
		if entry == nil || entry.isExpiredBy(now) {
			delete(entries, ip)
			c.removeReverse(ip, entry)
		}
	}
}

// upsertReverse updates the reverse DNS cache for ip with entry, if it expires
// later than the already-stored entry.
// It is assumed that entry includes ip.
// This needs a write lock
func (c *DNSCache) upsertReverse(ip string, entry *cacheEntry) {
	entries, exists := c.reverse[ip]
	if entries == nil || !exists {
		entries = make(map[string]*cacheEntry)
		c.reverse[ip] = entries
	}
	entries[entry.Name] = entry
}

// removeReverse removes the reference between ip and the name stored in entry.
// When no more refrences from ip to any name exist, the map entry is deleted
// outright.
// It is assumed that entry includes ip.
// This needs a write lock
func (c *DNSCache) removeReverse(ip string, entry *cacheEntry) {
	entries, exists := c.reverse[ip]
	if entries == nil || !exists {
		return
	}
	delete(entries, entry.Name)
	if len(entries) == 0 {
		delete(c.reverse, ip)
	}
}

// MarshalJSON serialises the set of DNS lookup cacheEntries needed to
// reconstruct this cache instance.
// Note: Expiration times are honored and the reconstructed cache instance is
// expected to return the same values as the original at that point in time.
func (c *DNSCache) MarshalJSON() ([]byte, error) {
	c.RLock()
	defer c.RUnlock()

	now := time.Now()

	// Collect all the still-valid entries
	lookups := make([]*cacheEntry, 0, len(c.forward))
	for _, entries := range c.forward {
		for _, entry := range entries {
			if !entry.isExpiredBy(now) {
				lookups = append(lookups, entry)
			}
		}
	}

	// Dedup the entries. They are created once and are immutable so the address
	// is a unique identifier.
	// We iterate through the list, keeping unique pointers. This is correct
	// because the list is sorted and, if two consecutive entries are the same,
	// it is safe to overwrite the second duplicate.
	sort.Slice(lookups, func(i, j int) bool {
		return uintptr(unsafe.Pointer(lookups[i])) < uintptr(unsafe.Pointer(lookups[j]))
	})

	deduped := lookups[:0] // len==0 but cap==cap(lookups)
	for readIdx, lookup := range lookups {
		if readIdx == 0 || deduped[len(deduped)-1] != lookups[readIdx] {
			deduped = append(deduped, lookup)
		}
	}

	// serialise into a JSON object array
	return json.Marshal(deduped)
}

// UnmarshalJSON rebuilds a DNSCache from serialized JSON.
// Note: This is destructive to any currect data. Use UpdateFromCache for bulk
// updates.
func (c *DNSCache) UnmarshalJSON(raw []byte) error {
	lookups := make([]*cacheEntry, 0)
	if err := json.Unmarshal(raw, &lookups); err != nil {
		return err
	}

	c.Lock()
	defer c.Unlock()

	c.forward = make(map[string]ipEntries)
	c.reverse = make(map[string]nameEntries)

	for _, newLookup := range lookups {
		c.updateWithEntry(newLookup)
	}

	return nil
}
