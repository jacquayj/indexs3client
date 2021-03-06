package handlers

import (
	"crypto/tls"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
)

// MaxRetries maximum number of retries
const MaxRetries = 10

type IndexdInfo struct {
	URL              string `json:"url"`
	Username         string `json:"username"`
	Password         string `json:"password"`
	ExtramuralBucket bool   `json:"extramural_bucket"`

	ExtramuralUploader         *string `json:"extramural_uploader"`
	ExtramuralUploaderS3Owner  bool    `json:"extramural_uploader_s3owner"`
	ExtramuralUploaderManifest *string `json:"extramural_uploader_manifest"`
	ExtramuralInitialMode      bool    `json:"extramural_initial_mode"` // If true, skips hash updates if record is already found in index
	ExtramuralFastMode         bool    `json:"extramural_fast_mode"`    // If true, always creates a new record for the object
}

type IndexdRecord struct {
	DID    string `json:"did"`
	BaseID string `json:"baseid"`
	Rev    string `json:"rev"`
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

func IndexS3ObjectEmbedded(s3objectURL string, indexdInfo *IndexdInfo, awsConfig *aws.Config) {
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	RunIndexS3Object(s3objectURL, indexdInfo, &AwsClient{awsConfig, nil})
}

func RunIndexS3Object(s3objectURL string, indexdInfo *IndexdInfo, client *AwsClient) {
	s3objectURL, _ = url.QueryUnescape(s3objectURL)
	u, err := url.Parse(s3objectURL)
	if err != nil {
		log.Panicf("Wrong url format %s\n", s3objectURL)
	}
	bucket, key := u.Host, u.Path
	var uuid, rev string
	var retries = 0
	// Create the indexd record if this is an ExtramuralBucket and it doesn't already exist
	if indexdInfo.ExtramuralBucket {

		var foundRecords searchResponse

		// Should we skip lookups for speed gains?
		if !indexdInfo.ExtramuralFastMode {
			// search indexd to see if the record already exists
			var err error
			foundRecords, err = SearchRecordByURL(indexdInfo, s3objectURL)
			if err != nil {
				log.Println(err)
				return
			}
		}

		// Should we create a blank record?
		if len(foundRecords) > 0 { // no

			// Skip hash update, this is an inital run
			if indexdInfo.ExtramuralInitialMode {
				log.Printf("Object already exists during initial index, skipping: %s. Done.", s3objectURL)
				return
			}

			// Find rev to update with hashes
			// foundRev, err := GetIndexdRecordRev(foundRecords[0].DID, indexdInfo.URL)
			// if err != nil {
			// 	log.Println(err)
			// 	return
			// }
			retries = 0
			uuid = foundRecords[0].DID
			for {
				rev, err = GetIndexdRecordRev(foundRecords[0].DID, indexdInfo.URL)
				if err != nil {
					retries++
					time.Sleep(5 * time.Second)
				} else {
					break
				}
				if retries == MaxRetries {
					log.Panicf("Can not get rev for %s. Detail %s", uuid, err)
				}
			}

		} else { // yes
			var uploader string

			if indexdInfo.ExtramuralUploader != nil {
				uploader = *(indexdInfo.ExtramuralUploader)
			} else if indexdInfo.ExtramuralUploaderS3Owner {
				s3owner, err := client.GetS3BucketOwner(bucket)
				if err != nil {
					panic(err) // Should always be able to fetch owner, something bad happened if not
				}

				uploader = s3owner
			} else if indexdInfo.ExtramuralUploaderManifest != nil {
				// Read from manifest, try to find uploader.
				// if fail, default to empty string
				oo, err := client.GetS3ObjectOutput(bucket, *indexdInfo.ExtramuralUploaderManifest)
				if err == nil {
					uploader = FindUploaderInManifest(key, oo.Body)
				} else {
					log.Println(err)
				}
			}

			body, _ := json.Marshal(struct {
				Uploader string `json:"uploader"`
				Filename string `json:"file_name"`
			}{
				uploader, filepath.Base(key),
			})

			indexdRecord, err := CreateBlankIndexdRecord(indexdInfo, body)
			if err != nil {
				log.Println(err)
				return
			}

			// To update hashes
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
		} else if len(split_key) == 3 {
			uuid = strings.Join(split_key[:len(split_key)-1], "/")
		} else {
			panic("Are you trying to index a non-Gen3 managed S3 bucket? Try setting 'extramural_bucket: true' in the config, no UUID found in object path.")
		}

		retries = 0
		for {
			rev, err = GetIndexdRecordRev(uuid, indexdInfo.URL)
			if err != nil {
				retries++
				time.Sleep(5 * time.Second)
			} else if rev == "" {
				log.Println("The file already has size and hashes")
				return
			} else {
				break
			}
			if retries == MaxRetries {
				log.Panicf("Can not get rev for %s. Detail %s", uuid, err)
			}
		}
	}

	log.Printf("Start to compute hashes for %s", key)
	hashes, objectSize, err := client.CalculateBasicHashes(bucket, key)

	if err != nil {
		log.Panicf("Can not compute hashes for %s. Detail %s ", key, err)
	}
	log.Printf("Finish to compute hashes for %s", key)

	body, _ := json.Marshal(struct {
		Size   int64     `json:"size"`
		URLs   []string  `json:"urls"`
		Hashes *HashInfo `json:"hashes"`
	}{
		objectSize, []string{s3objectURL}, hashes,
	})

	retries = 0
	for {
		resp, err := UpdateIndexdRecord(uuid, rev, indexdInfo, []byte(body))
		if err != nil {
			retries++
			log.Printf("Error: %s. Retry: %d", err, retries)
			time.Sleep(5 * time.Second)
		} else if resp.StatusCode != 200 {
			log.Printf("StatusCode: %d. Retry: %d", resp.StatusCode, retries)
			retries++
			time.Sleep(5 * time.Second)
		} else {
			log.Printf("Finish updating the record %s. Response Status: %d. Body %s", uuid, resp.StatusCode, body)
			break
		}

		if retries == MaxRetries {
			if err == nil {
				log.Panicf("Can not update %s with hash info. Body %s. Status code %d. Detail %s", uuid, body, resp.StatusCode, err)
			} else {
				log.Panicf("Can not update %s with hash info. Body %s. Detail %s", uuid, body, err)
			}
			break
		}
	}

	log.Printf("Done.")
}

// IndexS3Object indexes s3 object
// The fuction does several things. It first downloads the object from
// S3, computes size and hashes, and update indexd
func IndexS3Object(s3objectURL string) {
	indexdInfo, _ := getIndexServiceInfo()

	client, err := CreateNewAwsClient()
	if err != nil {
		log.Printf("Can not create AWS client. Detail %s\n\n", err)
		return
	}

	RunIndexS3Object(s3objectURL, indexdInfo, client)
}

func FindUploaderInManifest(object string, oo io.Reader) string {
	uploader := ""

	manifest := csv.NewReader(oo)
	manifestRecords, err := manifest.ReadAll()
	if err == nil {
		uploaderMap := make(map[string]string)

		for _, row := range manifestRecords {
			uploaderMap[row[0]] = row[1]
		}

		if val, ok := uploaderMap[object]; ok {
			uploader = val
		} else {
			log.Printf("Object %s not found in uploader manifest file", object)
		}
	} else {
		log.Println(err)
	}

	return uploader
}
