package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"cloud.google.com/go/storage"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
	"github.com/mailgun/mailgun-go/v4"
	"google.golang.org/api/option"
)

type Structmsg struct {
	SubmissionEmail string `json:"SubmissionEmail"`
	SubmissionUrl   string `json:"SubmissionUrl"`
	SubmissionId    string `json:"SubmissionId"`
	AssignmentId    string `json:"AssignmentId"`
	UserId          string `json:"UserId"`
}

func HandleRequest(ctx context.Context, event events.SNSEvent) (*string, error) {
	log.Println("Received event:")
	log.Println(event.Records[0].SNS.Message)

	msg := Structmsg{}
	json.Unmarshal([]byte(event.Records[0].SNS.Message), &msg)

	log.Println("Downloading from link")
	bFile, err := Download(msg.SubmissionUrl)
	filePath := ""
	mailStatus := 1
	if err != nil {
		mailStatus = -1
		log.Println("Error downloading file ", err)
	} else {
		log.Println("Uploading to GCP bucket")
		filePath = msg.AssignmentId + "/" + msg.UserId + "/" + msg.SubmissionId
		err = UploadToBucket(ctx, filePath, bFile)
		if err != nil {
			mailStatus = -2
			log.Println("Error uploading file ", err)
		}
	}

	body := GenerateBody(mailStatus, msg, filePath)
	log.Println("Sending mail")
	resp, id, err := SendMail(body, msg.SubmissionEmail)

	bEvent, merr := json.Marshal(event)
	if merr != nil {
		log.Println("Error marshalling ", merr)
	}

	log.Println("Inserting to dynamo db")
	InsertToDynamo(resp, id, err, mailStatus, string(bEvent))

	message := string(bEvent)
	return &message, nil
}

func InsertToDynamo(response, messageId string, err error, mailStatus int, requestMetadata string) {
	type Item struct {
		MessageId       string
		Response        string
		Error           string
		RequestMetadata string
		IsMailSent      bool
		MailStatus      int
	}

	sess := session.Must(session.NewSessionWithOptions(session.Options{}))
	svc := dynamodb.New(sess)

	serr := ""
	if err != nil {
		serr = err.Error()
	}

	item := Item{
		MessageId:       messageId,
		Response:        response,
		Error:           serr,
		RequestMetadata: requestMetadata,
		IsMailSent:      err == nil,
		MailStatus:      mailStatus,
	}
	av, err := dynamodbattribute.MarshalMap(item)
	if err != nil {
		log.Println("Error marshalling new item: %s", err)
	}

	input := &dynamodb.PutItemInput{
		Item:      av,
		TableName: aws.String(os.Getenv("MAIL_TABLE")),
	}

	_, err = svc.PutItem(input)
	if err != nil {
		log.Println("Error calling PutItem: %s", err)
	}
}

func UploadToBucket(ctx context.Context, submissionId string, bFile []byte) error {
	client, err := storage.NewClient(ctx, option.WithCredentialsJSON([]byte(os.Getenv("GCP_CREDS_JSON"))))
	if err != nil {
		log.Println("Error creating client", err)
		return err
	}

	bkt := client.Bucket(os.Getenv("BUCKET"))
	obj := bkt.Object(submissionId)
	w := obj.NewWriter(ctx)
	n, err := w.Write(bFile)
	if err != nil {
		log.Println("Error writing context: ", err, n)
		return err
	}

	err = w.Close()
	if err != nil {
		log.Println("Error closing writer: ", err)
		return err
	}
	err = client.Close()
	if err != nil {
		log.Println("Error closing client: ", err)
		return err
	}

	return err
}

func Download(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		log.Println("Error fetching URL ", err)
		return nil, err
	}

	if resp.Header.Get("Content-Type") != "application/zip" {
		log.Println("Zip file not provided")
		return nil, errors.New("Not a zip file")
	}

	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Println("Error reading response body ", err)
		return nil, err
	}

	return data, err
}

func GenerateBody(isSuccess int, message Structmsg, bucketPath string) string {
	if isSuccess == 1 {
		path := "gs://" + os.Getenv("BUCKET") + "/" + bucketPath
		return fmt.Sprint("Hello,\n\nThis message is to inform you that your assignment with id ", message.AssignmentId, " has been successfully uploaded and no further action is needed.\n\nThe uploaded path is: ", path, "  \n\nThank you!")
	}

	if isSuccess == -1 {
		return fmt.Sprint("Hello,\n\nThis message is to inform you that your assignment with id ", message.AssignmentId, " has NOT been uploaded due to invalid link or file type. Please modify the submission link or contact your TA for assistance to attempt and rectify the issue.\n\nThank you!")
	}

	if isSuccess == -2 {
		return fmt.Sprint("Hello,\n\nThis message is to inform you that your assignment with id ", message.AssignmentId, " has failed to upload to GCP bucket. Please contact your TA for assistance to attempt and rectify the issue.\n\nThank you!")
	}

	return fmt.Sprint("Hello,\n\nThis message is to inform you that your assignment with id ", message.AssignmentId, " has NOT been uploaded. Please contact your TA for assistance to attempt and rectify the issue.\n\nThank you!")

}

func SendMail(body string, recipient string) (string, string, error) {
	//return "sample", "sample2", nil
	mg := mailgun.NewMailgun(os.Getenv("MAILGUN_DOMAIN"), os.Getenv("MAILGUN_PVT_API_KEY"))
	message := mg.NewMessage(os.Getenv("SENDER"), os.Getenv("SUBJECT"), body, recipient)

	resp, id, err := mg.Send(context.Background(), message)
	if err != nil {
		log.Println("Error sending mail: ", resp, " ", id, " ", err.Error())
		return resp, id, err
	}

	return resp, id, err
}

func main() {
	lambda.Start(HandleRequest)
}
