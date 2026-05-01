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

type R2Client struct {
	R2_client  *s3.Client
	BucketName string
}

func InitS3SDK(server_cfg *cfg.Config) (R2Client, error) {
	var accessKeyId = server_cfg.R2_access_key_id
	var accessKeySecret = server_cfg.R2_access_key
	var r2_endpoint = server_cfg.R2_endpoint

	cfg, err := config.LoadDefaultConfig(context.TODO(),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKeyId, accessKeySecret, "")),
		config.WithRegion("auto"),
	)
	if err != nil {
		return R2Client{}, err
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(r2_endpoint)
	})
	return R2Client{
		R2_client:  client,
		BucketName: server_cfg.R2_bucket_name,
	}, nil
}

func PrettyPrintS3Objects(ol *s3.ListObjectsV2Output) {
	for _, object := range ol.Contents {
		obj, _ := json.MarshalIndent(object, "", "\t")
		fmt.Println(string(obj))
	}
}

func (r2 R2Client) GetObjectList() (*s3.ListObjectsV2Output, error) {
	objectList, err := r2.R2_client.ListObjectsV2(context.TODO(), &s3.ListObjectsV2Input{
		Bucket: &r2.BucketName,
	})
	if err != nil {
		return nil, err
	}
	return objectList, nil
}

func (r2 R2Client) GetObject(object_key string) ([]byte, error) {
	response, err := r2.R2_client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(r2.BucketName),
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

func (r2 R2Client) PutObject(key string, obj []byte) error {
	_, err := r2.R2_client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(r2.BucketName),
		Key:    aws.String(key),
		Body:   bytes.NewReader(obj),
	})
	return err
}

func (r2 R2Client) DeleteObject(key string) error {
	_, err := r2.R2_client.DeleteObject(context.TODO(), &s3.DeleteObjectInput{
		Bucket: aws.String(r2.BucketName),
		Key:    aws.String(key),
	})
	return err
}
