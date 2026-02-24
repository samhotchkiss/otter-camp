package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

const (
	multipartThreshold = int64(5 * 1024 * 1024)
	multipartPartSize  = 5 * 1024 * 1024
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

type S3Adapter struct {
	client s3Client
	bucket string
}

func NewS3(client s3Client, bucket string) *S3Adapter {
	return &S3Adapter{client: client, bucket: bucket}
}

func (a *S3Adapter) Put(ctx context.Context, key string, r io.Reader, opts PutOptions) error {
	if err := validateKey(key); err != nil {
		return err
	}

	if opts.ContentLength > multipartThreshold {
		return a.putMultipart(ctx, key, r, opts)
	}

	input := &s3.PutObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(key),
		Body:   r,
	}
	if opts.ContentType != "" {
		input.ContentType = aws.String(opts.ContentType)
	}
	if opts.ContentLength >= 0 {
		input.ContentLength = aws.Int64(opts.ContentLength)
	}

	if _, err := a.client.PutObject(ctx, input); err != nil {
		return fmt.Errorf("storage s3 put: %w", err)
	}
	return nil
}

func (a *S3Adapter) putMultipart(ctx context.Context, key string, r io.Reader, opts PutOptions) (retErr error) {
	createInput := &s3.CreateMultipartUploadInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(key),
	}
	if opts.ContentType != "" {
		createInput.ContentType = aws.String(opts.ContentType)
	}

	createOut, err := a.client.CreateMultipartUpload(ctx, createInput)
	if err != nil {
		return fmt.Errorf("storage s3 create multipart upload: %w", err)
	}

	uploadID := aws.ToString(createOut.UploadId)
	if uploadID == "" {
		return errors.New("storage s3 create multipart upload: empty upload id")
	}

	success := false
	defer func() {
		if success {
			return
		}
		_, _ = a.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
			Bucket:   aws.String(a.bucket),
			Key:      aws.String(key),
			UploadId: aws.String(uploadID),
		})
	}()

	partNumber := int32(1)
	parts := make([]types.CompletedPart, 0, 4)
	buf := make([]byte, multipartPartSize)

	for {
		readN, readErr := io.ReadFull(r, buf)
		if errors.Is(readErr, io.EOF) {
			break
		}
		if errors.Is(readErr, io.ErrUnexpectedEOF) {
			if readN == 0 {
				break
			}
		} else if readErr != nil {
			return fmt.Errorf("storage s3 multipart read: %w", readErr)
		}

		if readN == 0 {
			break
		}

		uploadOut, err := a.client.UploadPart(ctx, &s3.UploadPartInput{
			Bucket:        aws.String(a.bucket),
			Key:           aws.String(key),
			UploadId:      aws.String(uploadID),
			PartNumber:    aws.Int32(partNumber),
			Body:          bytes.NewReader(buf[:readN]),
			ContentLength: aws.Int64(int64(readN)),
		})
		if err != nil {
			return fmt.Errorf("storage s3 upload part %d: %w", partNumber, err)
		}

		parts = append(parts, types.CompletedPart{
			ETag:       uploadOut.ETag,
			PartNumber: aws.Int32(partNumber),
		})

		partNumber++
		if errors.Is(readErr, io.ErrUnexpectedEOF) {
			break
		}
	}

	if len(parts) == 0 {
		return errors.New("storage s3 multipart upload: no parts uploaded")
	}

	_, err = a.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(a.bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: parts,
		},
	})
	if err != nil {
		return fmt.Errorf("storage s3 complete multipart upload: %w", err)
	}

	success = true
	return nil
}

func (a *S3Adapter) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}

	out, err := a.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("storage s3 get: %w", err)
	}
	return out.Body, nil
}

func (a *S3Adapter) Delete(ctx context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}

	_, err := a.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("storage s3 delete: %w", err)
	}
	return nil
}

func (a *S3Adapter) Exists(ctx context.Context, key string) (bool, error) {
	if err := validateKey(key); err != nil {
		return false, err
	}

	_, err := a.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		return true, nil
	}
	if isS3NotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("storage s3 exists: %w", err)
}

func (a *S3Adapter) List(ctx context.Context, prefix string) ([]ObjectMeta, error) {
	if err := validatePrefix(prefix); err != nil {
		return nil, err
	}

	metas := make([]ObjectMeta, 0)
	var continuationToken *string

	for {
		out, err := a.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(a.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			return nil, fmt.Errorf("storage s3 list: %w", err)
		}

		for _, obj := range out.Contents {
			metas = append(metas, ObjectMeta{
				Key:          aws.ToString(obj.Key),
				Size:         aws.ToInt64(obj.Size),
				LastModified: aws.ToTime(obj.LastModified),
			})
		}

		if !aws.ToBool(out.IsTruncated) {
			break
		}
		continuationToken = out.NextContinuationToken
	}

	sort.Slice(metas, func(i, j int) bool {
		return metas[i].Key < metas[j].Key
	})

	return metas, nil
}

func isS3NotFound(err error) bool {
	var notFound *types.NotFound
	if errors.As(err, &notFound) {
		return true
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NotFound", "NoSuchKey", "404":
			return true
		}
	}

	return false
}
