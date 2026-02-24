package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestS3StorePutCallsPutObjectWithCorrectKey(t *testing.T) {
	mock := &mockS3Client{}
	store := newS3WithClient("bucket-1", mock)

	payload := []byte("hello")
	err := store.Put(context.Background(), "orgs/a/chat/1/file.txt", bytes.NewReader(payload), PutOptions{
		ContentType:   "text/plain",
		ContentLength: int64(len(payload)),
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if mock.putInput == nil {
		t.Fatal("PutObject not called")
	}
	if got := aws.ToString(mock.putInput.Key); got != "orgs/a/chat/1/file.txt" {
		t.Fatalf("Put key = %q, want %q", got, "orgs/a/chat/1/file.txt")
	}
}

func TestS3StoreGetCallsGetObjectWithCorrectKey(t *testing.T) {
	mock := &mockS3Client{
		getOutput: &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader([]byte("ok")))},
	}
	store := newS3WithClient("bucket-1", mock)

	body, err := store.Get(context.Background(), "orgs/a/chat/1/file.txt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer body.Close()

	if mock.getInput == nil {
		t.Fatal("GetObject not called")
	}
	if got := aws.ToString(mock.getInput.Key); got != "orgs/a/chat/1/file.txt" {
		t.Fatalf("Get key = %q, want %q", got, "orgs/a/chat/1/file.txt")
	}
}

func TestS3StoreDeletePropagatesErrors(t *testing.T) {
	mock := &mockS3Client{
		deleteErr: errors.New("boom"),
	}
	store := newS3WithClient("bucket-1", mock)

	err := store.Delete(context.Background(), "orgs/a/chat/1/file.txt")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestS3StoreListPropagatesErrors(t *testing.T) {
	mock := &mockS3Client{
		listErr: errors.New("boom"),
	}
	store := newS3WithClient("bucket-1", mock)

	_, err := store.List(context.Background(), "orgs/a/")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestS3StoreLargePutUsesMultipart(t *testing.T) {
	mock := &mockS3Client{
		createMultipartOutput: &s3.CreateMultipartUploadOutput{UploadId: aws.String("upload-1")},
	}
	store := newS3WithClient("bucket-1", mock)

	large := bytes.Repeat([]byte("x"), 6*1024*1024)
	err := store.Put(context.Background(), "orgs/a/runs/1/artifacts/large.bin", bytes.NewReader(large), PutOptions{
		ContentLength: int64(len(large)),
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if mock.createMultipartInput == nil {
		t.Fatal("CreateMultipartUpload not called")
	}
	if len(mock.uploadPartInputs) == 0 {
		t.Fatal("UploadPart not called")
	}
	if mock.completeMultipartInput == nil {
		t.Fatal("CompleteMultipartUpload not called")
	}
}

type mockS3Client struct {
	putInput  *s3.PutObjectInput
	putOutput *s3.PutObjectOutput
	putErr    error

	getInput  *s3.GetObjectInput
	getOutput *s3.GetObjectOutput
	getErr    error

	deleteInput  *s3.DeleteObjectInput
	deleteOutput *s3.DeleteObjectOutput
	deleteErr    error

	headInput  *s3.HeadObjectInput
	headOutput *s3.HeadObjectOutput
	headErr    error

	listInput  *s3.ListObjectsV2Input
	listOutput *s3.ListObjectsV2Output
	listErr    error

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
	m.putInput = params
	if m.putOutput == nil {
		m.putOutput = &s3.PutObjectOutput{}
	}
	return m.putOutput, m.putErr
}

func (m *mockS3Client) GetObject(_ context.Context, params *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	m.getInput = params
	if m.getOutput == nil {
		m.getOutput = &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(nil))}
	}
	return m.getOutput, m.getErr
}

func (m *mockS3Client) DeleteObject(_ context.Context, params *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	m.deleteInput = params
	if m.deleteOutput == nil {
		m.deleteOutput = &s3.DeleteObjectOutput{}
	}
	return m.deleteOutput, m.deleteErr
}

func (m *mockS3Client) HeadObject(_ context.Context, params *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	m.headInput = params
	if m.headOutput == nil {
		m.headOutput = &s3.HeadObjectOutput{LastModified: aws.Time(time.Now())}
	}
	return m.headOutput, m.headErr
}

func (m *mockS3Client) ListObjectsV2(_ context.Context, params *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	m.listInput = params
	if m.listOutput == nil {
		m.listOutput = &s3.ListObjectsV2Output{}
	}
	return m.listOutput, m.listErr
}

func (m *mockS3Client) CreateMultipartUpload(_ context.Context, params *s3.CreateMultipartUploadInput, _ ...func(*s3.Options)) (*s3.CreateMultipartUploadOutput, error) {
	m.createMultipartInput = params
	if m.createMultipartOutput == nil {
		m.createMultipartOutput = &s3.CreateMultipartUploadOutput{UploadId: aws.String("upload-default")}
	}
	return m.createMultipartOutput, m.createMultipartErr
}

func (m *mockS3Client) UploadPart(_ context.Context, params *s3.UploadPartInput, _ ...func(*s3.Options)) (*s3.UploadPartOutput, error) {
	m.uploadPartInputs = append(m.uploadPartInputs, params)
	if m.uploadPartOutput == nil {
		m.uploadPartOutput = &s3.UploadPartOutput{ETag: aws.String("etag-1")}
	}
	return m.uploadPartOutput, m.uploadPartErr
}

func (m *mockS3Client) CompleteMultipartUpload(_ context.Context, params *s3.CompleteMultipartUploadInput, _ ...func(*s3.Options)) (*s3.CompleteMultipartUploadOutput, error) {
	m.completeMultipartInput = params
	if m.completeMultipartOutput == nil {
		m.completeMultipartOutput = &s3.CompleteMultipartUploadOutput{}
	}
	return m.completeMultipartOutput, m.completeMultipartErr
}

func (m *mockS3Client) AbortMultipartUpload(_ context.Context, params *s3.AbortMultipartUploadInput, _ ...func(*s3.Options)) (*s3.AbortMultipartUploadOutput, error) {
	m.abortMultipartInput = params
	if m.abortMultipartOutput == nil {
		m.abortMultipartOutput = &s3.AbortMultipartUploadOutput{}
	}
	return m.abortMultipartOutput, m.abortMultipartErr
}
