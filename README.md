# serverless

Run the following build command and ensure that the binary has the name bootstrap (which is needed to run custom lambda run time in this case it is al2):
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o bootstrap -tags lambda.norpc main.go

Zip the file calling the resultant zip as lambda.zip

Upload the zip code to the lambda function