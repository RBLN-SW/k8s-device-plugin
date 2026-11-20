package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// buses is sorted, normalized, unique and nonempty (allocationBusIDs).
func rsdAliasName(buses []string) string {
	digest := rsdSetDigest(buses)
	return rsdSetName(digest[:])
}

func rsdSetDigest(buses []string) [sha256.Size]byte {
	return sha256.Sum256([]byte(strings.Join(buses, ",")))
}

func rsdSetName(digest []byte) string {
	return "set-" + hex.EncodeToString(digest)
}

func unlinkRSDFile(path string) error {
	err := unix.Unlink(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// A cached HostPath must never resolve to a group that another phase has
// repurposed. Retire every intersecting different alias before changing the
// hardware. Index multi-device aliases by each member, so {A,B} is retired
// even when the next request contains only B. All new names use the set
// prefix, so a leftover alias from a previous draft cannot satisfy a start.
// The caller holds the node writer lock. Update the index before publishing
// the node so a retry after process loss can always find a published alias.
func retireOverlappingAliases(staging string, buses []string) error {
	dir := filepath.Dir(staging)
	next := rsdSetDigest(buses)
	for _, bus := range buses {
		index := filepath.Join(dir, ".member-"+bus)
		old, err := os.ReadFile(index)
		if err == nil {
			if len(old) != sha256.Size {
				return fmt.Errorf("invalid RSD membership index for %s", bus)
			}
			if bytes.Equal(old, next[:]) {
				continue
			}
			if err := unlinkRSDFile(filepath.Join(dir, rsdSetName(old))); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		marker := filepath.Join(staging, "membership")
		if err := os.WriteFile(marker, next[:], 0o600); err != nil {
			return err
		}
		if err := os.Rename(marker, index); err != nil {
			return err
		}
	}
	return nil
}
