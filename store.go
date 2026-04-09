package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("already exists")
)

type User struct {
	ID           string
	Email        string
	GoogleID     string
	DisplayName  string
	PasswordHash string
	Verified     bool
}

type SessionData struct {
	UserID    string
	ExpiresAt time.Time
}

type Store interface {
	GetUserByID(ctx context.Context, id string) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	GetUserByGoogleID(ctx context.Context, googleID string) (*User, error)
	CreateUser(ctx context.Context, u *User) error
	VerifyUser(ctx context.Context, userID string) error
	UpdateUser(ctx context.Context, userID, displayName, passwordHash string) error
	DeleteUser(ctx context.Context, u *User) error

	CreateVerificationToken(ctx context.Context, token, userID string, expiresAt time.Time) error
	ConsumeVerificationToken(ctx context.Context, token string) (string, error)

	GetPortfolio(ctx context.Context, userID string) ([]Stock, []Category, error)
	PutStock(ctx context.Context, userID string, s Stock) error
	DeleteStock(ctx context.Context, userID, ticker string) error
	UpdateStockCategory(ctx context.Context, userID, ticker, category string) error
	ReplaceStocks(ctx context.Context, userID string, stocks []Stock) error
	PutCategory(ctx context.Context, userID string, c Category) error
	DeleteCategory(ctx context.Context, userID, name string) error
	ReplaceCategories(ctx context.Context, userID string, cats []Category) error

	CreateSession(ctx context.Context, token, userID string, expiresAt time.Time) error
	GetSession(ctx context.Context, token string) (*SessionData, error)
	DeleteSession(ctx context.Context, token string) error
}

// ---- DynamoDB implementation ----

type DynamoStore struct {
	client *dynamodb.Client
	table  string
}

func NewDynamoStore(ctx context.Context, endpoint, region, table string) (*DynamoStore, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}
	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
	})
	store := &DynamoStore{client: client, table: table}
	return store, store.ensureTable(ctx)
}

func (d *DynamoStore) ensureTable(ctx context.Context) error {
	_, err := d.client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: &d.table})
	if err == nil {
		return nil
	}
	var notFound *types.ResourceNotFoundException
	if !errors.As(err, &notFound) {
		return fmt.Errorf("describing table: %w", err)
	}
	if _, err = d.client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: &d.table,
		AttributeDefinitions: []types.AttributeDefinition{
			{AttributeName: aws.String("PK"), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: aws.String("SK"), AttributeType: types.ScalarAttributeTypeS},
		},
		KeySchema: []types.KeySchemaElement{
			{AttributeName: aws.String("PK"), KeyType: types.KeyTypeHash},
			{AttributeName: aws.String("SK"), KeyType: types.KeyTypeRange},
		},
		BillingMode: types.BillingModePayPerRequest,
	}); err != nil {
		return fmt.Errorf("creating table: %w", err)
	}
	if _, err = d.client.UpdateTimeToLive(ctx, &dynamodb.UpdateTimeToLiveInput{
		TableName: &d.table,
		TimeToLiveSpecification: &types.TimeToLiveSpecification{
			AttributeName: aws.String("TTL"),
			Enabled:       aws.Bool(true),
		},
	}); err != nil {
		return fmt.Errorf("enabling TTL: %w", err)
	}
	return nil
}

// ---- User ----

func (d *DynamoStore) GetUserByID(ctx context.Context, id string) (*User, error) {
	out, err := d.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &d.table,
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: "USER#" + id},
			"SK": &types.AttributeValueMemberS{Value: "PROFILE"},
		},
	})
	if err != nil {
		return nil, err
	}
	if out.Item == nil {
		return nil, ErrNotFound
	}
	var item struct {
		UserID       string `dynamodbav:"UserID"`
		Email        string `dynamodbav:"Email"`
		GoogleID     string `dynamodbav:"GoogleID"`
		DisplayName  string `dynamodbav:"DisplayName"`
		PasswordHash string `dynamodbav:"PasswordHash"`
		Verified     bool   `dynamodbav:"Verified"`
	}
	if err := attributevalue.UnmarshalMap(out.Item, &item); err != nil {
		return nil, err
	}
	return &User{
		ID: item.UserID, Email: item.Email, GoogleID: item.GoogleID,
		DisplayName: item.DisplayName, PasswordHash: item.PasswordHash,
		Verified: item.Verified,
	}, nil
}

func (d *DynamoStore) getUserByRef(ctx context.Context, pk string) (*User, error) { //nolint
	out, err := d.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &d.table,
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk},
			"SK": &types.AttributeValueMemberS{Value: "USER_REF"},
		},
	})
	if err != nil {
		return nil, err
	}
	if out.Item == nil {
		return nil, ErrNotFound
	}
	var ref struct {
		UserID string `dynamodbav:"UserID"`
	}
	if err := attributevalue.UnmarshalMap(out.Item, &ref); err != nil {
		return nil, err
	}
	return d.GetUserByID(ctx, ref.UserID)
}

func (d *DynamoStore) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	return d.getUserByRef(ctx, "EMAIL#"+email)
}

func (d *DynamoStore) GetUserByGoogleID(ctx context.Context, googleID string) (*User, error) {
	return d.getUserByRef(ctx, "GOOGLE#"+googleID)
}

func (d *DynamoStore) CreateUser(ctx context.Context, u *User) error {
	profileItem, err := attributevalue.MarshalMap(map[string]any{
		"PK": "USER#" + u.ID, "SK": "PROFILE",
		"UserID": u.ID, "Email": u.Email, "GoogleID": u.GoogleID,
		"DisplayName": u.DisplayName, "PasswordHash": u.PasswordHash,
		"Verified": u.Verified,
	})
	if err != nil {
		return err
	}
	items := []types.TransactWriteItem{{
		Put: &types.Put{
			TableName:           &d.table,
			Item:                profileItem,
			ConditionExpression: aws.String("attribute_not_exists(PK)"),
		},
	}}
	if u.Email != "" {
		ref, _ := attributevalue.MarshalMap(map[string]any{
			"PK": "EMAIL#" + u.Email, "SK": "USER_REF", "UserID": u.ID,
		})
		items = append(items, types.TransactWriteItem{Put: &types.Put{
			TableName:           &d.table,
			Item:                ref,
			ConditionExpression: aws.String("attribute_not_exists(PK)"),
		}})
	}
	if u.GoogleID != "" {
		ref, _ := attributevalue.MarshalMap(map[string]any{
			"PK": "GOOGLE#" + u.GoogleID, "SK": "USER_REF", "UserID": u.ID,
		})
		items = append(items, types.TransactWriteItem{Put: &types.Put{
			TableName:           &d.table,
			Item:                ref,
			ConditionExpression: aws.String("attribute_not_exists(PK)"),
		}})
	}
	_, err = d.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: items})
	if err != nil {
		var tce *types.TransactionCanceledException
		if errors.As(err, &tce) {
			return ErrConflict
		}
		return err
	}
	return nil
}

// ---- Portfolio ----

func (d *DynamoStore) GetPortfolio(ctx context.Context, userID string) ([]Stock, []Category, error) {
	var stocks []Stock
	var cats []Category
	var lastKey map[string]types.AttributeValue

	for {
		input := &dynamodb.QueryInput{
			TableName:              &d.table,
			KeyConditionExpression: aws.String("PK = :pk"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk": &types.AttributeValueMemberS{Value: "USER#" + userID},
			},
		}
		if lastKey != nil {
			input.ExclusiveStartKey = lastKey
		}
		out, err := d.client.Query(ctx, input)
		if err != nil {
			return nil, nil, err
		}
		for _, item := range out.Items {
			sk, ok := item["SK"].(*types.AttributeValueMemberS)
			if !ok {
				continue
			}
			switch {
			case strings.HasPrefix(sk.Value, "STOCK#"):
				var s struct {
					Ticker   string `dynamodbav:"Ticker"`
					Name     string `dynamodbav:"Name"`
					Category string `dynamodbav:"Category"`
				}
				attributevalue.UnmarshalMap(item, &s)
				stocks = append(stocks, Stock{Ticker: s.Ticker, Name: s.Name, Category: s.Category})
			case strings.HasPrefix(sk.Value, "CATEGORY#"):
				var c struct {
					Name  string `dynamodbav:"Name"`
					Emoji string `dynamodbav:"Emoji"`
					Order int    `dynamodbav:"Order"`
				}
				attributevalue.UnmarshalMap(item, &c)
				cats = append(cats, Category{Name: c.Name, Emoji: c.Emoji, Order: c.Order})
			}
		}
		if out.LastEvaluatedKey == nil {
			break
		}
		lastKey = out.LastEvaluatedKey
	}
	return stocks, cats, nil
}

func (d *DynamoStore) PutStock(ctx context.Context, userID string, s Stock) error {
	item, err := attributevalue.MarshalMap(map[string]any{
		"PK": "USER#" + userID, "SK": "STOCK#" + s.Ticker,
		"Ticker": s.Ticker, "Name": s.Name, "Category": s.Category,
	})
	if err != nil {
		return err
	}
	_, err = d.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           &d.table,
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(PK)"),
	})
	if err != nil {
		if isDynamoConditionalFailed(err) {
			return ErrConflict
		}
		return err
	}
	return nil
}

func (d *DynamoStore) DeleteStock(ctx context.Context, userID, ticker string) error {
	_, err := d.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &d.table,
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: "USER#" + userID},
			"SK": &types.AttributeValueMemberS{Value: "STOCK#" + ticker},
		},
		ConditionExpression: aws.String("attribute_exists(PK)"),
	})
	if err != nil {
		if isDynamoConditionalFailed(err) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func (d *DynamoStore) UpdateStockCategory(ctx context.Context, userID, ticker, category string) error {
	_, err := d.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &d.table,
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: "USER#" + userID},
			"SK": &types.AttributeValueMemberS{Value: "STOCK#" + ticker},
		},
		UpdateExpression:    aws.String("SET Category = :cat"),
		ConditionExpression: aws.String("attribute_exists(PK)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":cat": &types.AttributeValueMemberS{Value: category},
		},
	})
	if err != nil {
		if isDynamoConditionalFailed(err) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func (d *DynamoStore) ReplaceStocks(ctx context.Context, userID string, stocks []Stock) error {
	pk := "USER#" + userID
	existing, err := d.queryBySKPrefix(ctx, pk, "STOCK#")
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		dels := make([]types.WriteRequest, len(existing))
		for i, item := range existing {
			dels[i] = types.WriteRequest{DeleteRequest: &types.DeleteRequest{
				Key: map[string]types.AttributeValue{"PK": &types.AttributeValueMemberS{Value: pk}, "SK": item["SK"]},
			}}
		}
		if err := d.batchWrite(ctx, dels); err != nil {
			return err
		}
	}
	if len(stocks) > 0 {
		puts := make([]types.WriteRequest, len(stocks))
		for i, s := range stocks {
			item, _ := attributevalue.MarshalMap(map[string]any{
				"PK": pk, "SK": "STOCK#" + s.Ticker,
				"Ticker": s.Ticker, "Name": s.Name, "Category": s.Category,
			})
			puts[i] = types.WriteRequest{PutRequest: &types.PutRequest{Item: item}}
		}
		if err := d.batchWrite(ctx, puts); err != nil {
			return err
		}
	}
	return nil
}

func (d *DynamoStore) PutCategory(ctx context.Context, userID string, c Category) error {
	item, err := attributevalue.MarshalMap(map[string]any{
		"PK": "USER#" + userID, "SK": "CATEGORY#" + c.Name,
		"Name": c.Name, "Emoji": c.Emoji, "Order": c.Order,
	})
	if err != nil {
		return err
	}
	_, err = d.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           &d.table,
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(PK)"),
	})
	if err != nil {
		if isDynamoConditionalFailed(err) {
			return ErrConflict
		}
		return err
	}
	return nil
}

func (d *DynamoStore) DeleteCategory(ctx context.Context, userID, name string) error {
	_, err := d.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &d.table,
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: "USER#" + userID},
			"SK": &types.AttributeValueMemberS{Value: "CATEGORY#" + name},
		},
		ConditionExpression: aws.String("attribute_exists(PK)"),
	})
	if err != nil {
		if isDynamoConditionalFailed(err) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func (d *DynamoStore) ReplaceCategories(ctx context.Context, userID string, cats []Category) error {
	pk := "USER#" + userID
	existing, err := d.queryBySKPrefix(ctx, pk, "CATEGORY#")
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		dels := make([]types.WriteRequest, len(existing))
		for i, item := range existing {
			dels[i] = types.WriteRequest{DeleteRequest: &types.DeleteRequest{
				Key: map[string]types.AttributeValue{"PK": &types.AttributeValueMemberS{Value: pk}, "SK": item["SK"]},
			}}
		}
		if err := d.batchWrite(ctx, dels); err != nil {
			return err
		}
	}
	if len(cats) > 0 {
		puts := make([]types.WriteRequest, len(cats))
		for i, c := range cats {
			item, _ := attributevalue.MarshalMap(map[string]any{
				"PK": pk, "SK": "CATEGORY#" + c.Name,
				"Name": c.Name, "Emoji": c.Emoji, "Order": c.Order,
			})
			puts[i] = types.WriteRequest{PutRequest: &types.PutRequest{Item: item}}
		}
		if err := d.batchWrite(ctx, puts); err != nil {
			return err
		}
	}
	return nil
}

// ---- Sessions ----

func (d *DynamoStore) CreateSession(ctx context.Context, token, userID string, expiresAt time.Time) error {
	item, err := attributevalue.MarshalMap(map[string]any{
		"PK": "SESSION#" + token, "SK": "SESSION",
		"UserID": userID,
		"TTL":    expiresAt.Unix(),
	})
	if err != nil {
		return err
	}
	_, err = d.client.PutItem(ctx, &dynamodb.PutItemInput{TableName: &d.table, Item: item})
	return err
}

func (d *DynamoStore) GetSession(ctx context.Context, token string) (*SessionData, error) {
	out, err := d.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &d.table,
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: "SESSION#" + token},
			"SK": &types.AttributeValueMemberS{Value: "SESSION"},
		},
	})
	if err != nil {
		return nil, err
	}
	if out.Item == nil {
		return nil, ErrNotFound
	}
	var item struct {
		UserID string `dynamodbav:"UserID"`
		TTL    int64  `dynamodbav:"TTL"`
	}
	if err := attributevalue.UnmarshalMap(out.Item, &item); err != nil {
		return nil, err
	}
	exp := time.Unix(item.TTL, 0)
	if time.Now().After(exp) {
		return nil, ErrNotFound
	}
	return &SessionData{UserID: item.UserID, ExpiresAt: exp}, nil
}

func (d *DynamoStore) DeleteSession(ctx context.Context, token string) error {
	_, err := d.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &d.table,
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: "SESSION#" + token},
			"SK": &types.AttributeValueMemberS{Value: "SESSION"},
		},
	})
	return err
}

// ---- Email verification ----

func (d *DynamoStore) UpdateUser(ctx context.Context, userID, displayName, passwordHash string) error {
	_, err := d.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &d.table,
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: "USER#" + userID},
			"SK": &types.AttributeValueMemberS{Value: "PROFILE"},
		},
		UpdateExpression: aws.String("SET DisplayName = :n, PasswordHash = :p"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":n": &types.AttributeValueMemberS{Value: displayName},
			":p": &types.AttributeValueMemberS{Value: passwordHash},
		},
	})
	return err
}

func (d *DynamoStore) DeleteUser(ctx context.Context, u *User) error {
	// Delete stocks and categories
	if err := d.ReplaceStocks(ctx, u.ID, nil); err != nil {
		return err
	}
	if err := d.ReplaceCategories(ctx, u.ID, nil); err != nil {
		return err
	}
	// Delete refs and profile in a transaction
	items := []types.TransactWriteItem{{
		Delete: &types.Delete{
			TableName: &d.table,
			Key: map[string]types.AttributeValue{
				"PK": &types.AttributeValueMemberS{Value: "USER#" + u.ID},
				"SK": &types.AttributeValueMemberS{Value: "PROFILE"},
			},
		},
	}}
	if u.Email != "" {
		items = append(items, types.TransactWriteItem{Delete: &types.Delete{
			TableName: &d.table,
			Key: map[string]types.AttributeValue{
				"PK": &types.AttributeValueMemberS{Value: "EMAIL#" + u.Email},
				"SK": &types.AttributeValueMemberS{Value: "USER_REF"},
			},
		}})
	}
	if u.GoogleID != "" {
		items = append(items, types.TransactWriteItem{Delete: &types.Delete{
			TableName: &d.table,
			Key: map[string]types.AttributeValue{
				"PK": &types.AttributeValueMemberS{Value: "GOOGLE#" + u.GoogleID},
				"SK": &types.AttributeValueMemberS{Value: "USER_REF"},
			},
		}})
	}
	_, err := d.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: items})
	return err
}

func (d *DynamoStore) VerifyUser(ctx context.Context, userID string) error {
	_, err := d.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &d.table,
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: "USER#" + userID},
			"SK": &types.AttributeValueMemberS{Value: "PROFILE"},
		},
		UpdateExpression: aws.String("SET Verified = :v"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":v": &types.AttributeValueMemberBOOL{Value: true},
		},
	})
	return err
}

func (d *DynamoStore) CreateVerificationToken(ctx context.Context, token, userID string, expiresAt time.Time) error {
	item, err := attributevalue.MarshalMap(map[string]any{
		"PK": "VERIFY#" + token, "SK": "VERIFY",
		"UserID": userID,
		"TTL":    expiresAt.Unix(),
	})
	if err != nil {
		return err
	}
	_, err = d.client.PutItem(ctx, &dynamodb.PutItemInput{TableName: &d.table, Item: item})
	return err
}

func (d *DynamoStore) ConsumeVerificationToken(ctx context.Context, token string) (string, error) {
	out, err := d.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &d.table,
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: "VERIFY#" + token},
			"SK": &types.AttributeValueMemberS{Value: "VERIFY"},
		},
		ReturnValues: types.ReturnValueAllOld,
	})
	if err != nil {
		return "", err
	}
	if len(out.Attributes) == 0 {
		return "", ErrNotFound
	}
	var item struct {
		UserID string `dynamodbav:"UserID"`
		TTL    int64  `dynamodbav:"TTL"`
	}
	if err := attributevalue.UnmarshalMap(out.Attributes, &item); err != nil {
		return "", err
	}
	if time.Now().After(time.Unix(item.TTL, 0)) {
		return "", ErrNotFound
	}
	return item.UserID, nil
}

// ---- helpers ----

func (d *DynamoStore) queryBySKPrefix(ctx context.Context, pk, skPrefix string) ([]map[string]types.AttributeValue, error) {
	var items []map[string]types.AttributeValue
	var lastKey map[string]types.AttributeValue
	for {
		input := &dynamodb.QueryInput{
			TableName:              &d.table,
			KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk":     &types.AttributeValueMemberS{Value: pk},
				":prefix": &types.AttributeValueMemberS{Value: skPrefix},
			},
		}
		if lastKey != nil {
			input.ExclusiveStartKey = lastKey
		}
		out, err := d.client.Query(ctx, input)
		if err != nil {
			return nil, err
		}
		items = append(items, out.Items...)
		if out.LastEvaluatedKey == nil {
			break
		}
		lastKey = out.LastEvaluatedKey
	}
	return items, nil
}

func (d *DynamoStore) batchWrite(ctx context.Context, requests []types.WriteRequest) error {
	const maxBatch = 25
	for i := 0; i < len(requests); i += maxBatch {
		end := i + maxBatch
		if end > len(requests) {
			end = len(requests)
		}
		_, err := d.client.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
			RequestItems: map[string][]types.WriteRequest{d.table: requests[i:end]},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func isDynamoConditionalFailed(err error) bool {
	var ccf *types.ConditionalCheckFailedException
	return errors.As(err, &ccf)
}
