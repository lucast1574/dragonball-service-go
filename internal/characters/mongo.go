package characters

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

type mongoStore struct{ collection *mongo.Collection }

// NewMongoSaver supplies the endpoint's persistence function using MongoDB.
func NewMongoSaver(collection *mongo.Collection) func(context.Context, character) error {
	return mongoStore{collection: collection}.save
}

func (s mongoStore) save(ctx context.Context, c character) error {
	_, err := s.collection.InsertOne(ctx, c)
	if mongo.IsDuplicateKeyError(err) {
		return errDuplicate
	}
	if mongo.IsTimeout(err) {
		return fmt.Errorf("%w: %w", context.DeadlineExceeded, err)
	}
	return err
}
