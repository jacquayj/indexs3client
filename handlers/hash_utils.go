package handlers

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"log"
	"sync"
)

const ChunkSize = 1024 * 1024 * 64

type HashInfo struct {
	Crc32c string
	Md5    string
	Sha1   string
	Sha256 string
	Sha512 string
}

// HashCollection contains hashes
type HashCollection struct {
	Crc32c hash.Hash
	Md5    hash.Hash
	Sha1   hash.Hash
	Sha256 hash.Hash
	Sha512 hash.Hash
}

// CreateNewHashCollection creates a new HashCollection
func CreateNewHashCollection() *HashCollection {
	h := new(HashCollection)
	h.Crc32c = crc32.New(crc32.MakeTable(crc32.Castagnoli))
	h.Md5 = md5.New()
	h.Sha1 = sha1.New()
	h.Sha256 = sha256.New()
	h.Sha512 = sha512.New()
	return h
}

func (h *HashCollection) Reset() {
	h.Crc32c.Reset()
	h.Md5.Reset()
	h.Sha1.Reset()
	h.Sha256.Reset()
	h.Sha512.Reset()
}

// CalculateBasicHashes generates hashes of aws bucket object
func CalculateBasicHashes(client *AwsClient, bucket string, key string) (*HashInfo, int64, error) {
	hashCollection := CreateNewHashCollection()

	objectSize, err := GetObjectSize(client, bucket, key)
	if err != nil {
		log.Printf("Fail to get object size of %s. Detail %s\n\n", key, err)
		return nil, -1, err
	}
	log.Printf("Size %d", *objectSize)

	start := int64(0)
	step := int64(ChunkSize)
	for {
		chunkRange := fmt.Sprintf("bytes: %d-%d", start, minOf(start+step, *objectSize-1))

		buff, err := GetChunkDataFromS3(client, bucket, key, chunkRange)
		if err != nil {
			log.Printf("Can not stream chunk data of %s. Detail %s\n\n", key, err)
			return nil, -1, err
		}

		log.Print(len(buff))

		//hashCollection, err = UpdateBasicHashes(hashCollection, buff)

		if err != nil {
			log.Printf("Can not compute hashes. Detail %s\n\n", err)
		}
		start = minOf(start+step, *objectSize-1) + 1
		if start >= *objectSize {
			break
		}
	}

	return &HashInfo{
		Crc32c: hex.EncodeToString(hashCollection.Crc32c.Sum(nil)),
		Md5:    hex.EncodeToString(hashCollection.Md5.Sum(nil)),
		Sha1:   hex.EncodeToString(hashCollection.Sha1.Sum(nil)),
		Sha256: hex.EncodeToString(hashCollection.Sha256.Sum(nil)),
		Sha512: hex.EncodeToString(hashCollection.Sha512.Sum(nil)),
	}, *objectSize, nil

}

// UpdateBasicHashes updates a hashes collection
func UpdateBasicHashes(hashCollection *HashCollection, rd []byte) (*HashCollection, error) {

	hashCollection.Reset()
	multiWriter := io.MultiWriter(hashCollection.Crc32c, hashCollection.Md5, hashCollection.Sha1, hashCollection.Sha256, hashCollection.Sha512)
	_, err := multiWriter.Write(rd)

	return hashCollection, err
}

type multiCWriter struct {
	writers []io.Writer
}

func (t *multiCWriter) Write(p []byte) (n int, err error) {
	type data struct {
		n   int
		err error
	}

	results := make([]data, len(t.writers))

	var wg sync.WaitGroup
	for idx, w := range t.writers {
		wg.Add(1)
		go func(wr io.Writer, p []byte, res *data) {
			defer wg.Done()
			res.n, res.err = wr.Write(p)
			if res.n != len(p) {
				res.err = io.ErrShortWrite
			}
		}(w, p, &results[idx])
	}
	wg.Wait()
	for idx := range t.writers {
		if results[idx].err != nil {
			return results[idx].n, results[idx].err
		}
	}

	return len(p), nil
}
