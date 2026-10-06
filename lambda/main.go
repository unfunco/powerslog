package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/unfunco/powerslog"
)

func newHandler(logger *slog.Logger) func(context.Context, events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	return func(ctx context.Context, event events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
		logger.InfoContext(ctx, "Request received",
			slog.String("method", event.HTTPMethod),
			slog.String("resource", event.Resource),
		)

		return events.APIGatewayProxyResponse{
			StatusCode: http.StatusNoContent,
		}, nil
	}
}

func main() {
	logger := slog.New(powerslog.NewHandler(os.Stdout, nil))

	lambda.Start(newHandler(logger))
}
