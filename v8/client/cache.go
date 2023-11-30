package client

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/jcmturner/gokrb5/v8/messages"
	"github.com/jcmturner/gokrb5/v8/types"
)

// Cache for service tickets held by the client.
type Cache struct {
	Entries map[string]CacheEntry
	mux     sync.RWMutex
}

func key(cname string, spn string) string {
	// use base64 to ensure ':' is not in cname or spn
	return base64.StdEncoding.EncodeToString([]byte(cname)) + ":" + base64.StdEncoding.EncodeToString([]byte(spn))
}

// CacheEntry holds details for a cache entry.
type CacheEntry struct {
	CName      types.PrincipalName
	SPN        string
	Ticket     messages.Ticket `json:"-"`
	AuthTime   time.Time
	StartTime  time.Time
	EndTime    time.Time
	RenewTill  time.Time
	SessionKey types.EncryptionKey `json:"-"`
}

// NewCache creates a new client ticket cache instance.
func NewCache() *Cache {
	return &Cache{
		Entries: map[string]CacheEntry{},
	}
}

// getEntry returns a cache entry that matches the SPN.
func (c *Cache) getEntry(cname, spn string) (CacheEntry, bool) {
	c.mux.RLock()
	defer c.mux.RUnlock()
	e, ok := (*c).Entries[key(cname, spn)]
	return e, ok
}

// JSON returns information about the cached service tickets in a JSON format.
func (c *Cache) JSON() (string, error) {
	c.mux.RLock()
	defer c.mux.RUnlock()
	var es []CacheEntry
	keys := make([]string, 0, len(c.Entries))
	for k := range c.Entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		es = append(es, c.Entries[k])
	}
	b, err := json.MarshalIndent(&es, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// addEntry adds a ticket to the cache.
func (c *Cache) addEntry(cname types.PrincipalName, tkt messages.Ticket, authTime, startTime, endTime, renewTill time.Time, sessionKey types.EncryptionKey) CacheEntry {
	spn := tkt.SName.PrincipalNameString()
	k := key(cname.PrincipalNameString(), spn)
	c.mux.Lock()
	defer c.mux.Unlock()
	(*c).Entries[k] = CacheEntry{
		CName:      cname,
		SPN:        spn,
		Ticket:     tkt,
		AuthTime:   authTime,
		StartTime:  startTime,
		EndTime:    endTime,
		RenewTill:  renewTill,
		SessionKey: sessionKey,
	}
	return c.Entries[k]
}

// clear deletes all the cache entries
func (c *Cache) clear() {
	c.mux.Lock()
	defer c.mux.Unlock()
	for k := range c.Entries {
		delete(c.Entries, k)
	}
}

// RemoveEntry removes the cache entry for the defined SPN.
func (c *Cache) RemoveEntry(cname, spn string) {
	c.mux.Lock()
	defer c.mux.Unlock()
	delete(c.Entries, key(cname, spn))
}

// GetCachedTicket returns a ticket from the cache for the SPN.
// Only a ticket that is currently valid will be returned.
func (cl *Client) GetCachedTicket(cname, spn string) (messages.Ticket, types.EncryptionKey, bool) {
	if e, ok := cl.cache.getEntry(cname, spn); ok {
		// If within time window of ticket return it
		if time.Now().UTC().After(e.StartTime) && time.Now().UTC().Before(e.EndTime) {
			cl.Log("ticket received from cache for %s", spn)
			return e.Ticket, e.SessionKey, true
		} else if time.Now().UTC().Before(e.RenewTill) {
			e, err := cl.renewTicket(e)
			if err != nil {
				return e.Ticket, e.SessionKey, false
			}
			return e.Ticket, e.SessionKey, true
		}
	}
	var tkt messages.Ticket
	var key types.EncryptionKey
	return tkt, key, false
}

// renewTicket renews a cache entry ticket.
// To renew from outside the client package use GetCachedTicket
func (cl *Client) renewTicket(e CacheEntry) (CacheEntry, error) {
	// TODO(adphi): delegation ????
	spn := e.Ticket.SName
	_, _, err := cl.TGSREQGenerateAndExchange(spn, e.Ticket.Realm, e.Ticket, e.SessionKey, true)
	if err != nil {
		return e, err
	}
	e, ok := cl.cache.getEntry(e.CName.PrincipalNameString(), e.Ticket.SName.PrincipalNameString())
	if !ok {
		return e, errors.New("ticket was not added to cache")
	}
	cl.Log("ticket renewed for %s (EndTime: %v)", spn.PrincipalNameString(), e.EndTime)
	return e, nil
}
