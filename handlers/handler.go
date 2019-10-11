package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type IndexdInfo struct {
	URL              string `url`
	Username         string `username`
	Password         string `password`
	ExtramuralBucket bool   `extramural_bucket`
}

type IndexdRecord struct {
	DID    string `did`
	BaseID string `baseid`
	Rev    string `rev`
}

func minOf(vars ...int64) int64 {
	min := vars[0]

	for _, i := range vars {
		if min > i {
			min = i
		}
	}

	return min
}
func getIndexServiceInfo() (*IndexdInfo, error) {
	indexdInfo := new(IndexdInfo)
	if err := json.Unmarshal([]byte(os.Getenv("CONFIG_FILE")), indexdInfo); err != nil {
		return nil, errors.New("Enviroiment variable CONFIG_FILE is not set correctly")
	}
	return indexdInfo, nil
}

// IndexS3Object indexes s3 object
// The fuction does several things. It first downloads the object from
// S3, computes size and hashes, and update indexd
func IndexS3Object(s3objectURL string) {
	s3objectURL, _ = url.QueryUnescape(s3objectURL)
	u, err := url.Parse(s3objectURL)
	if err != nil {
		log.Printf("Wrong url format %s\n", s3objectURL)
		return
	}
	bucket, key := u.Host, u.Path

	indexdInfo, _ := getIndexServiceInfo()

	client, err := CreateNewAwsClient()
	if err != nil {
		log.Printf("Can not create AWS client. Detail %s\n\n", err)
		return
	}

	log.Printf("Start to compute hashes for %s", key)
	hashes, objectSize, err := CalculateBasicHashes(client, bucket, key)

	if err != nil {
		log.Printf("Can not compute hashes for %s. Detail %s ", key, err)
		return
	}
	log.Printf("Finish to compute hashes for %s", key)

	var uuid, rev string

	// Create the indexd record if this is an ExtramuralBucket and it doesn't already exist
	if indexdInfo.ExtramuralBucket {

		// search indexd to see if the record already exists
		if foundIndexdRecord, err := GetIndexdRecordByHash(indexdInfo, hashes); err == nil {
			uuid = foundIndexdRecord.DID
			rev = foundIndexdRecord.Rev
		} else {
			body := fmt.Sprintf(`{"uploader": "%s", "file_name": "%s"}`,
				"indexs3object-ExtramuralBucket", filepath.Base(key))

			indexdRecord, err := CreateBlankIndexdRecord(indexdInfo, []byte(body))
			if err != nil {
				log.Println(err)
				return
			}

			uuid = indexdRecord.DID
			rev = indexdRecord.Rev
		}

	} else {
		// key looks like one of these:
		//
		//     <uuid>/<filename>
		//     <dataguid>/<uuid>/<filename>
		//
		// we want to keep the `<dataguid>/<uuid>` part
		split_key := strings.Split(key, "/")
		if len(split_key) == 2 {
			uuid = split_key[0]
		} else {
			uuid = strings.Join(split_key[:len(split_key)-1], "/")
		}

		rev, err = GetIndexdRecordRev(uuid, indexdInfo.URL)
		if err != nil {
			log.Println(err)
			return
		}
	}

	body := fmt.Sprintf(`{"size": %d, "urls": ["%s"], "hashes": {"md5": "%s", "sha1":"%s", "sha256": "%s", "sha512": "%s", "crc": "%s"}}`,
		objectSize, s3objectURL, hashes.Md5, hashes.Sha1, hashes.Sha256, hashes.Sha512, hashes.Crc32c)
	resp, err := UpdateIndexdRecord(uuid, rev, indexdInfo, []byte(body))
	if err != nil {
		log.Println(err)
	}

	log.Printf("Finish updating the record. Response Status: %s", resp.Status)

}
