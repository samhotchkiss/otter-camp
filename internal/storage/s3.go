package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type s3Client interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	CreateMultipartUpload(ctx context.Context, params *s3.CreateMultipartUploadInput, optFns ...func(*s3.Options)) (*s3.CreateMultipartUploadOutput, error)
	UploadPart(ctx context.Context, params *s3.UploadPartInput, optFns ...func(*s3.Options)) (*s3.UploadPartOutput, error)
	CompleteMultipartUpload(ctx context.Context, params *s3.CompleteMultipartUploadInput, optFns ...func(*s3.Options)) (*s3.CompleteMultipartUploadOutput, error)
	AbortMultipartUpload(ctx context.Context, params *s3.AbortMultipartUploadInput, optFns ...func(*s3.Options)) (*s3.AbortMultipartUploadOutput, error)
}

type S3Store struct {
	client             s3Client
	bucket             string
	multipartThreshold int64
	partSize           int64
}

func NewS3(cfg Config) (*S3Store, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(
		context.Background(),
		awsconfig.WithRegion(cfg.S3Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.S3AccessKeyID, cfg.S3SecretAccessKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(opts *s3.Options) {
		if cfg.S3Endpoint != "" {
			opts.BaseEndpoint = aws.String(cfg.S3Endpoint)
			opts.UsePathStyle = true
		}
	})

	return newS3WithClient(cfg.S3Bucket, client), nil
}

func newS3WithClient(bucket string, client s3Client) *S3Store {
	return &S3Store{
		client:             client,
		bucket:             bucket,
		multipartThreshold: multipartThresholdSize,
		partSize:           multipartThresholdSize,
	}
}

func (s *S3Store) Put(ctx context.Context, key string, r io.Reader, opts PutOptions) error {
	if err := validateKey(key); err != nil {
		return err
	}

	if opts.ContentLength > s.multipartThreshold {
		return s.putMultipart(ctx, key, r, opts)
	}

	input := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   r,
	}
	if opts.ContentType != "" {
		input.ContentType = aws.String(opts.ContentType)
	}
	if opts.ContentLength >= 0 {
		input.ContentLength = aws.Int64(opts.ContentLength)
	}

	if _, err := s.client.PutObject(ctx, input); err != nil {
		return fmt.Errorf("s3 put object: %w", err)
	}
	return nil
}

func (s *S3Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}

	output, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("s3 get object: %w", err)
	}
	return output.Body, nil
}

func (s *S3Store) Delete(ctx context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}

	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}); err != nil {
		return fmt.Errorf("s3 delete object: %w", err)
	}
	return nil
}

func (s *S3Store) Exists(ctx context.Context, key string) (bool, error) {
	if err := validateKey(key); err != nil {
		return false, err
	}

	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		return true, nil
	}
	if isS3NotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("s3 head object: %w", err)
}

func (s *S3Store) List(ctx context.Context, prefix string) ([]ObjectMeta, error) {
	if err := validatePrefix(prefix); err != nil {
		return nil, err
	}

	objects := make([]ObjectMeta, 0, 16)
	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(prefix),
	}

	for {
		output, err := s.client.ListObjectsV2(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("s3 list objects: %w", err)
		}

		for _, object := range output.Contents {
			objects = append(objects, ObjectMeta{
				Key:          aws.ToString(object.Key),
				Size:         aws.ToInt64(object.Size),
				LastModified: aws.ToTime(object.LastModified),
			})
		}

		if !aws.ToBool(output.IsTruncated) {
			break
		}
		input.ContinuationToken = output.NextContinuationToken
	}

	sort.Slice(objects, func(i, j int) bool {
		return objects[i].Key < objects[j].Key
	})

	return objects, nil
}

func (s *S3Store) putMultipart(ctx context.Context, key string, r io.Reader, opts PutOptions) (retErr error) {
	createInput := &s3.CreateMultipartUploadInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}
	if opts.ContentType != "" {
		createInput.ContentType = aws.String(opts.ContentType)
	}

	createOutput, err := s.client.CreateMultipartUpload(ctx, createInput)
	if err != nil {
		return fmt.Errorf("s3 create multipart upload: %w", err)
	}

	uploadID := aws.ToString(createOutput.UploadId)
	defer func() {
		if retErr == nil {
			return
		}
		_, _ = s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
			Bucket:   aws.String(s.bucket),
			Key:      aws.String(key),
			UploadId: aws.String(uploadID),
		})
	}()

	parts := make([]s3types.CompletedPart, 0, 2)
	partNumber := int32(1)
	buf := make([]byte, s.partSize)

	for {
		n, readErr := io.ReadFull(r, buf)
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) {
			return fmt.Errorf("read multipart body: %w", readErr)
		}
		if n == 0 {
			break
		}

		chunk := make([]byte, n)
		copy(chunk, buf[:n])
		uploadPartOutput, err := s.client.UploadPart(ctx, &s3.UploadPartInput{
			Bucket:        aws.String(s.bucket),
			Key:           aws.String(key),
			UploadId:      aws.String(uploadID),
			PartNumber:    aws.Int32(partNumber),
			Body:          bytes.NewReader(chunk),
			ContentLength: aws.Int64(int64(n)),
		})
		if err != nil {
			return fmt.Errorf("s3 upload part %d: %w", partNumber, err)
		}

		parts = append(parts, s3types.CompletedPart{
			ETag:       uploadPartOutput.ETag,
			PartNumber: aws.Int32(partNumber),
		})
		partNumber++

		if errors.Is(readErr, io.ErrUnexpectedEOF) {
			break
		}
	}

	if len(parts) == 0 {
		return fmt.Errorf("multipart upload produced no parts")
	}

	_, err = s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(s.bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
		MultipartUpload: &s3types.CompletedMultipartUpload{
			Parts: parts,
		},
	})
	if err != nil {
		return fmt.Errorf("s3 complete multipart upload: %w", err)
	}

	return nil
}

func isS3NotFound(err error) bool {
	var noSuchKey *s3types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return true
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == "NotFound" || apiErr.ErrorCode() == "NoSuchKey"
	}

	return false
}
