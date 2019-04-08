package handlers

import (
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
)

// AWS sesssion wrapper
type AwsClient struct {
	session *session.Session
}

// CreateNewSession creates an aws s3 session
func CreateNewAwsClient() (*AwsClient, error) {
	client := new(AwsClient)

	sess, err := session.NewSession(&aws.Config{
		Region: aws.String(os.Getenv("AWS_REGION")),
		Credentials: credentials.NewStaticCredentials(
			os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY"), ""),
	})

	if err != nil {
		return nil, err
	}

	client.session = sess
	return client, nil
}

// GetChunkDataFromS3 downloads chunk data from s3
func GetChunkDataFromS3(client *AwsClient, bucket string, key string, byteRange string) ([]byte, error) {

	svc := s3.New(client.session)
	input := &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Range:  aws.String(byteRange),
	}

	_, err := svc.GetObject(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case s3.ErrCodeNoSuchKey:
				fmt.Println(s3.ErrCodeNoSuchKey, aerr.Error())
			default:
				fmt.Println(aerr.Error())
			}
		} else {
			// Print the error, cast err to awserr.Error to get the Code and
			// Message from an error.
			fmt.Println(err.Error())
		}
		return nil, err
	}
	// body, err := ioutil.ReadAll(result.Body)
	// if err != nil {
	// 	fmt.Println(err.Error())
	// }
	return nil, nil

}

// GetObjectSize returns object size in bytes
func GetObjectSize(client *AwsClient, bucket string, key string) (*int64, error) {
	svc := s3.New(client.session)
	input := &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}

	result, err := svc.HeadObject(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			default:
				fmt.Println(aerr.Error())
			}
		} else {
			// Print the error, cast err to awserr.Error to get the Code and
			// Message from an error.
			fmt.Println(err.Error())
		}
		return nil, err
	}
	return result.ContentLength, nil
}
