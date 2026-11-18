module example.com/payments

go 1.23

require (
	github.com/stripe/stripe-go/v76 v76.3.0
	github.com/aws/aws-sdk-go-v2/service/sqs v1.38.0
	golang.org/x/text v0.20.0
)

replace example.com/private => ../private
