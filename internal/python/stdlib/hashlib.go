// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package stdlib

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"hash"

	"github.com/pijalu/gpython/py"

	"github.com/pijalu/goa/internal/python/compat"
)

// Hash wraps a cryptographic hash result.
type Hash struct {
	sum []byte
}

var hashType = py.NewType("Hash", `Hash

Hash object returned by hashlib functions.`)

func init() {
	py.RegisterModule(&py.ModuleImpl{
		Info: py.ModuleInfo{
			Name: "hashlib",
			Doc:  "hashlib — secure hash and message digest algorithms",
		},
		Methods: []*py.Method{
			py.MustNewMethod("md5", hashlibMD5, 0, `md5(data) -> Hash

Return an MD5 hash object.`),
			py.MustNewMethod("sha1", hashlibSHA1, 0, `sha1(data) -> Hash

Return a SHA-1 hash object.`),
			py.MustNewMethod("sha256", hashlibSHA256, 0, `sha256(data) -> Hash

Return a SHA-256 hash object.`),
			py.MustNewMethod("sha512", hashlibSHA512, 0, `sha512(data) -> Hash

Return a SHA-512 hash object.`),
		},
	})

	hashType.Dict["hexdigest"] = py.MustNewMethod("hexdigest", hashHexdigest, 0, `hexdigest() -> str

Return the digest as a hex string.`)
	hashType.Dict["digest"] = py.MustNewMethod("digest", hashDigest, 0, `digest() -> bytes

Return the digest as bytes.`)
}

// Type returns the Hash type.
func (h *Hash) Type() *py.Type { return hashType }

func hashHexdigest(self py.Object, args py.Tuple) (py.Object, error) {
	if len(args) != 0 {
		return nil, compat.TooManyArgsError("hexdigest", 0)
	}
	h, ok := self.(*Hash)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "expected Hash, got %s", self.Type().Name)
	}
	return py.String(hex.EncodeToString(h.sum)), nil
}

func hashDigest(self py.Object, args py.Tuple) (py.Object, error) {
	if len(args) != 0 {
		return nil, compat.TooManyArgsError("digest", 0)
	}
	h, ok := self.(*Hash)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "expected Hash, got %s", self.Type().Name)
	}
	return py.Bytes(h.sum), nil
}

func hashlibMD5(self py.Object, args py.Tuple) (py.Object, error) {
	return makeHash(args, md5.New())
}

func hashlibSHA1(self py.Object, args py.Tuple) (py.Object, error) {
	return makeHash(args, sha1.New())
}

func hashlibSHA256(self py.Object, args py.Tuple) (py.Object, error) {
	return makeHash(args, sha256.New())
}

func hashlibSHA512(self py.Object, args py.Tuple) (py.Object, error) {
	return makeHash(args, sha512.New())
}

func makeHash(args py.Tuple, h hash.Hash) (py.Object, error) {
	var data py.Object
	if err := py.UnpackTuple(args, nil, "hash", 1, 1, &data); err != nil {
		return nil, err
	}
	b, err := compat.AsBytes(data, "hash")
	if err != nil {
		return nil, err
	}
	_, _ = h.Write(b)
	return &Hash{sum: h.Sum(nil)}, nil
}

// Ensure Hash implements py.Object.
var _ py.Object = (*Hash)(nil)
