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

type contextKey string

const loggerContextKey contextKey = "logger"

func handler(ctx context.Context, event events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	logger := ctx.Value(loggerContextKey).(*slog.Logger)
	logger.Info("Request received", slog.Any("event", event))

	return events.APIGatewayProxyResponse{
		StatusCode: http.StatusNoContent,
	}, nil
}

func main() {
	jsonHandler := slog.NewJSONHandler(os.Stdout, nil)
	powerslogHandler := powerslog.NewHandler(jsonHandler)
	logger := slog.New(powerslogHandler)

	ctx := context.WithValue(context.Background(), loggerContextKey, logger)
	lambda.StartWithOptions(handler, lambda.WithContext(ctx))
}
