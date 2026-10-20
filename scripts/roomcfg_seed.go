//go:build ignore

// SPDX-License-Identifier: GPL-3.0-or-later
//
// Offline test helper for scripts/e2e.sh: seed a bilirec `subscribes.db` with a
// subscribed room so that the room-config REST API can be exercised without
// network access. `PUT /room/<id>/config` requires the room to be subscribed and
// the real subscribe path calls Bilibili's live API.
//
// It writes bbolt bucket `Room_Subscriptions` key `<decimal room id>` with a
// gob-encoded `subscribe.RoomConfig` value. Only the v0.2.0 fields are emitted
// (all zero values); the FEATURES-V2 pointer fields are absent -> nil -> inherit,
// which is exactly the "old DB record" case the contract says must decode
// without migration. gob decoding is structural, so the newer struct keeps
// working and the new fields stay nil.
//
// Run it from the repo root (which is the Go module; deps resolve through
// go.mod):
//
//	go run scripts/roomcfg_seed.go <database-dir> <room-id> [room-id...]
//
// The file carries `//go:build ignore` so it is never part of a normal build or
// test run.
package main

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	bolt "go.etcd.io/bbolt"
)

const bucketName = "Room_Subscriptions"

// seedRoomConfig mirrors subscribe.RoomConfig as of v0.2.0 (structurally, gob
// only transmits field names/types, so importing the internal package is
// unnecessary and would not be allowed outside the module).
type seedRoomConfig struct {
	AutoRecord            bool
	Notify                bool
	RecordDurationMinutes int
	Qn                    int
	OnlyAudio             bool
	RecordDanmaku         bool
	StreamProfiles        []string
	DeleteOldestOnLowDisk bool
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: roomcfg_seed <database-dir> <room-id> [room-id...]\n")
		os.Exit(2)
	}
	dir := os.Args[1]
	rooms := os.Args[2:]

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(&seedRoomConfig{}); err != nil {
		fmt.Fprintf(os.Stderr, "roomcfg_seed: gob encode: %v\n", err)
		os.Exit(1)
	}
	value := buf.Bytes()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "roomcfg_seed: mkdir %s: %v\n", dir, err)
		os.Exit(1)
	}
	dbPath := filepath.Join(dir, "subscribes.db")
	db, err := bolt.Open(dbPath, 0o600, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "roomcfg_seed: open %s: %v\n", dbPath, err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(bucketName))
		if err != nil {
			return err
		}
		for _, raw := range rooms {
			roomID, err := strconv.Atoi(raw)
			if err != nil {
				return fmt.Errorf("invalid room id %q", raw)
			}
			// fmt.Append(nil, roomID) / []byte(strconv.Itoa(roomID)): the
			// subscribe service stores decimal ASCII room IDs as keys.
			if err := bucket.Put([]byte(strconv.Itoa(roomID)), value); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "roomcfg_seed: update %s: %v\n", dbPath, err)
		os.Exit(1)
	}
	fmt.Printf("roomcfg_seed: %s rooms=%v (%d-byte gob RoomConfig)\n", dbPath, rooms, len(value))
}
