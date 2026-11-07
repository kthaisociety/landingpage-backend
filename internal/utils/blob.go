package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	cfg "backend/internal/config"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// BlobStore reads and writes one bucket through the S3 API. Each file is stored
// under its BlobData.BlobId; the database holds the rest.
type BlobStore struct {
	client     *s3.Client
	BucketName string
}

// InitS3SDK returns the store for the "files" bucket (S3_BUCKET_FILES).
func InitS3SDK(server_cfg *cfg.Config) (BlobStore, error) {
	s := server_cfg.S3

	cfg, err := config.LoadDefaultConfig(context.TODO(),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(s.AccessKeyID, s.SecretAccessKey, "")),
		config.WithRegion(s.Region),
	)
	if err != nil {
		return BlobStore{}, err
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(s.Endpoint)
		// endpoint/bucket/key rather than bucket.endpoint/key: GleSYS is
		// addressed path-style (infrastructure's terraform/glesys).
		o.UsePathStyle = true
	})
	return BlobStore{
		client:     client,
		BucketName: s.BucketFiles,
	}, nil
}

func PrettyPrintS3Objects(ol *s3.ListObjectsV2Output) {
	for _, object := range ol.Contents {
		obj, _ := json.MarshalIndent(object, "", "\t")
		fmt.Println(string(obj))
	}
}

func (b BlobStore) GetObjectList() (*s3.ListObjectsV2Output, error) {
	objectList, err := b.client.ListObjectsV2(context.TODO(), &s3.ListObjectsV2Input{
		Bucket: &b.BucketName,
	})
	if err != nil {
		return nil, err
	}
	return objectList, nil
}

func (b BlobStore) GetObject(object_key string) ([]byte, error) {
	response, err := b.client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(b.BucketName),
		Key:    aws.String(object_key),
	})
	if err != nil {
		return []byte{}, err
	}
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return []byte{}, nil
	}
	return data, nil
}

func (b BlobStore) PutObject(key string, obj []byte) error {
	_, err := b.client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(b.BucketName),
		Key:    aws.String(key),
		Body:   bytes.NewReader(obj),
	})
	return err
}

func (b BlobStore) DeleteObject(key string) error {
	_, err := b.client.DeleteObject(context.TODO(), &s3.DeleteObjectInput{
		Bucket: aws.String(b.BucketName),
		Key:    aws.String(key),
	})
	return err
}
