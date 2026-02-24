package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

func TestS3AdapterPutCallsPutObjectAndGetCallsGetObject(t *testing.T) {
	mock := &mockS3Client{
		getObjectOutput: &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader("payload"))},
	}
	adapter := NewS3(mock, "bucket-a")
	ctx := context.Background()

	if err := adapter.Put(ctx, "orgs/abc/chat/item.txt", strings.NewReader("payload"), PutOptions{ContentLength: int64(len("payload"))}); err != nil {
		t.Fatalf("Put returned error: %v", err)
	}

	if mock.putObjectInput == nil {
		t.Fatal("PutObject was not called")
	}
	if got := aws.ToString(mock.putObjectInput.Bucket); got != "bucket-a" {
		t.Fatalf("PutObject bucket = %q, want %q", got, "bucket-a")
	}
	if got := aws.ToString(mock.putObjectInput.Key); got != "orgs/abc/chat/item.txt" {
		t.Fatalf("PutObject key = %q, want %q", got, "orgs/abc/chat/item.txt")
	}

	r, err := adapter.Get(ctx, "orgs/abc/chat/item.txt")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	defer r.Close()

	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	if string(body) != "payload" {
		t.Fatalf("Get body = %q, want %q", string(body), "payload")
	}
	if mock.getObjectInput == nil {
		t.Fatal("GetObject was not called")
	}
	if got := aws.ToString(mock.getObjectInput.Key); got != "orgs/abc/chat/item.txt" {
		t.Fatalf("GetObject key = %q, want %q", got, "orgs/abc/chat/item.txt")
	}
}

func TestS3AdapterDeleteListAndExists(t *testing.T) {
	now := time.Now().UTC()
	mock := &mockS3Client{
		headObjectOutput: &s3.HeadObjectOutput{},
		listOutputs: []*s3.ListObjectsV2Output{
			{
				Contents:              []types.Object{{Key: aws.String("orgs/a/z"), Size: aws.Int64(2), LastModified: aws.Time(now)}},
				IsTruncated:           aws.Bool(true),
				NextContinuationToken: aws.String("next"),
			},
			{
				Contents:    []types.Object{{Key: aws.String("orgs/a/a"), Size: aws.Int64(1), LastModified: aws.Time(now)}},
				IsTruncated: aws.Bool(false),
			},
		},
	}
	adapter := NewS3(mock, "bucket-a")
	ctx := context.Background()

	exists, err := adapter.Exists(ctx, "orgs/a/a")
	if err != nil {
		t.Fatalf("Exists returned error: %v", err)
	}
	if !exists {
		t.Fatal("Exists = false, want true")
	}

	metas, err := adapter.List(ctx, "orgs/a/")
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("List size = %d, want 2", len(metas))
	}
	if metas[0].Key != "orgs/a/a" || metas[1].Key != "orgs/a/z" {
		t.Fatalf("List order = [%s, %s], want sorted keys", metas[0].Key, metas[1].Key)
	}

	if err := adapter.Delete(ctx, "orgs/a/a"); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if mock.deleteObjectInput == nil {
		t.Fatal("DeleteObject was not called")
	}
}

func TestS3AdapterExistsHandlesNotFound(t *testing.T) {
	mock := &mockS3Client{headObjectErr: &smithy.GenericAPIError{Code: "NotFound", Message: "missing"}}
	adapter := NewS3(mock, "bucket-a")

	exists, err := adapter.Exists(context.Background(), "orgs/a/missing")
	if err != nil {
		t.Fatalf("Exists returned error: %v", err)
	}
	if exists {
		t.Fatal("Exists = true, want false")
	}
}

func TestS3AdapterMultipartForLargeObject(t *testing.T) {
	mock := &mockS3Client{
		createMultipartOutput: &s3.CreateMultipartUploadOutput{UploadId: aws.String("upload-1")},
		uploadPartOutput:      &s3.UploadPartOutput{ETag: aws.String("etag-1")},
	}
	adapter := NewS3(mock, "bucket-a")

	payload := bytes.Repeat([]byte("x"), int(multipartThreshold)+123)
	err := adapter.Put(context.Background(), "orgs/a/large.bin", bytes.NewReader(payload), PutOptions{ContentLength: int64(len(payload))})
	if err != nil {
		t.Fatalf("Put returned error: %v", err)
	}

	if mock.createMultipartInput == nil {
		t.Fatal("CreateMultipartUpload was not called")
	}
	if len(mock.uploadPartInputs) < 2 {
		t.Fatalf("UploadPart call count = %d, want >= 2", len(mock.uploadPartInputs))
	}
	if mock.completeMultipartInput == nil {
		t.Fatal("CompleteMultipartUpload was not called")
	}
	if mock.putObjectInput != nil {
		t.Fatal("PutObject should not be used for large payloads")
	}
}

func TestS3AdapterErrorPropagation(t *testing.T) {
	cases := []struct {
		name string
		run  func(*S3Adapter) error
		err  error
	}{
		{
			name: "put",
			err:  errors.New("put boom"),
			run: func(a *S3Adapter) error {
				return a.Put(context.Background(), "orgs/a/k", strings.NewReader("x"), PutOptions{ContentLength: 1})
			},
		},
		{
			name: "get",
			err:  errors.New("get boom"),
			run: func(a *S3Adapter) error {
				_, err := a.Get(context.Background(), "orgs/a/k")
				return err
			},
		},
		{
			name: "delete",
			err:  errors.New("delete boom"),
			run: func(a *S3Adapter) error {
				return a.Delete(context.Background(), "orgs/a/k")
			},
		},
		{
			name: "list",
			err:  errors.New("list boom"),
			run: func(a *S3Adapter) error {
				_, err := a.List(context.Background(), "orgs/a/")
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockS3Client{}
			switch tc.name {
			case "put":
				mock.putObjectErr = tc.err
			case "get":
				mock.getObjectErr = tc.err
			case "delete":
				mock.deleteObjectErr = tc.err
			case "list":
				mock.listErr = tc.err
			}

			adapter := NewS3(mock, "bucket-a")
			err := tc.run(adapter)
			if err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.err.Error()) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.err.Error())
			}
		})
	}
}

type mockS3Client struct {
	putObjectInput *s3.PutObjectInput
	putObjectErr   error

	getObjectInput  *s3.GetObjectInput
	getObjectOutput *s3.GetObjectOutput
	getObjectErr    error

	deleteObjectInput *s3.DeleteObjectInput
	deleteObjectErr   error

	headObjectInput  *s3.HeadObjectInput
	headObjectOutput *s3.HeadObjectOutput
	headObjectErr    error

	listInputs  []*s3.ListObjectsV2Input
	listOutputs []*s3.ListObjectsV2Output
	listErr     error

	createMultipartInput  *s3.CreateMultipartUploadInput
	createMultipartOutput *s3.CreateMultipartUploadOutput
	createMultipartErr    error

	uploadPartInputs []*s3.UploadPartInput
	uploadPartOutput *s3.UploadPartOutput
	uploadPartErr    error

	completeMultipartInput  *s3.CompleteMultipartUploadInput
	completeMultipartOutput *s3.CompleteMultipartUploadOutput
	completeMultipartErr    error

	abortMultipartInput  *s3.AbortMultipartUploadInput
	abortMultipartOutput *s3.AbortMultipartUploadOutput
	abortMultipartErr    error
}

func (m *mockS3Client) PutObject(_ context.Context, params *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	m.putObjectInput = params
	if m.putObjectErr != nil {
		return nil, m.putObjectErr
	}
	return &s3.PutObjectOutput{}, nil
}

func (m *mockS3Client) GetObject(_ context.Context, params *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	m.getObjectInput = params
	if m.getObjectErr != nil {
		return nil, m.getObjectErr
	}
	if m.getObjectOutput != nil {
		return m.getObjectOutput, nil
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(""))}, nil
}

func (m *mockS3Client) DeleteObject(_ context.Context, params *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	m.deleteObjectInput = params
	if m.deleteObjectErr != nil {
		return nil, m.deleteObjectErr
	}
	return &s3.DeleteObjectOutput{}, nil
}

func (m *mockS3Client) HeadObject(_ context.Context, params *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	m.headObjectInput = params
	if m.headObjectErr != nil {
		return nil, m.headObjectErr
	}
	if m.headObjectOutput != nil {
		return m.headObjectOutput, nil
	}
	return &s3.HeadObjectOutput{}, nil
}

func (m *mockS3Client) ListObjectsV2(_ context.Context, params *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	m.listInputs = append(m.listInputs, params)
	if m.listErr != nil {
		return nil, m.listErr
	}
	if len(m.listOutputs) == 0 {
		return &s3.ListObjectsV2Output{}, nil
	}
	out := m.listOutputs[0]
	m.listOutputs = m.listOutputs[1:]
	return out, nil
}

func (m *mockS3Client) CreateMultipartUpload(_ context.Context, params *s3.CreateMultipartUploadInput, _ ...func(*s3.Options)) (*s3.CreateMultipartUploadOutput, error) {
	m.createMultipartInput = params
	if m.createMultipartErr != nil {
		return nil, m.createMultipartErr
	}
	if m.createMultipartOutput != nil {
		return m.createMultipartOutput, nil
	}
	return &s3.CreateMultipartUploadOutput{UploadId: aws.String("upload")}, nil
}

func (m *mockS3Client) UploadPart(_ context.Context, params *s3.UploadPartInput, _ ...func(*s3.Options)) (*s3.UploadPartOutput, error) {
	m.uploadPartInputs = append(m.uploadPartInputs, params)
	if m.uploadPartErr != nil {
		return nil, m.uploadPartErr
	}
	if m.uploadPartOutput != nil {
		return m.uploadPartOutput, nil
	}
	return &s3.UploadPartOutput{ETag: aws.String("etag")}, nil
}

func (m *mockS3Client) CompleteMultipartUpload(_ context.Context, params *s3.CompleteMultipartUploadInput, _ ...func(*s3.Options)) (*s3.CompleteMultipartUploadOutput, error) {
	m.completeMultipartInput = params
	if m.completeMultipartErr != nil {
		return nil, m.completeMultipartErr
	}
	if m.completeMultipartOutput != nil {
		return m.completeMultipartOutput, nil
	}
	return &s3.CompleteMultipartUploadOutput{}, nil
}

func (m *mockS3Client) AbortMultipartUpload(_ context.Context, params *s3.AbortMultipartUploadInput, _ ...func(*s3.Options)) (*s3.AbortMultipartUploadOutput, error) {
	m.abortMultipartInput = params
	if m.abortMultipartErr != nil {
		return nil, m.abortMultipartErr
	}
	if m.abortMultipartOutput != nil {
		return m.abortMultipartOutput, nil
	}
	return &s3.AbortMultipartUploadOutput{}, nil
}
