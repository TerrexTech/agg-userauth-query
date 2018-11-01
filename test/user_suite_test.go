package test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/Shopify/sarama"
	"github.com/TerrexTech/agg-userauth-query/user"
	"github.com/TerrexTech/go-commonutils/commonutil"
	"github.com/TerrexTech/go-eventstore-models/model"
	"github.com/TerrexTech/go-kafkautils/kafka"
	"github.com/TerrexTech/uuuid"
	"github.com/joho/godotenv"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
)

func Byf(s string, args ...interface{}) {
	By(fmt.Sprintf(s, args...))
}

func TestUser(t *testing.T) {
	log.Println("Reading environment file")
	err := godotenv.Load("../.env")
	if err != nil {
		err = errors.Wrap(err,
			".env file not found, env-vars will be read as set in environment",
		)
		log.Println(err)
	}

	missingVar, err := commonutil.ValidateEnv(
		"KAFKA_BROKERS",
		"KAFKA_CONSUMER_EVENT_GROUP",

		"KAFKA_CONSUMER_EVENT_TOPIC",
		"KAFKA_CONSUMER_EVENT_QUERY_GROUP",
		"KAFKA_CONSUMER_EVENT_QUERY_TOPIC",

		"KAFKA_PRODUCER_EVENT_TOPIC",
		"KAFKA_PRODUCER_EVENT_QUERY_TOPIC",
		"KAFKA_PRODUCER_RESPONSE_TOPIC",
	)

	if err != nil {
		err = errors.Wrapf(
			err,
			"Env-var %s is required for testing, but is not set", missingVar,
		)
		log.Fatalln(err)
	}

	RegisterFailHandler(Fail)
	RunSpecs(t, "UserAggregate Suite")
}

var _ = Describe("UserAggregate", func() {
	var (
		kafkaBrokers          []string
		eventsTopic           string
		producerResponseTopic string

		mockUser  *user.User
		mockEvent *model.Event
	)

	BeforeSuite(func() {
		kafkaBrokers = *commonutil.ParseHosts(
			os.Getenv("KAFKA_BROKERS"),
		)
		eventsTopic = os.Getenv("KAFKA_PRODUCER_EVENT_TOPIC")
		producerResponseTopic = os.Getenv("KAFKA_PRODUCER_RESPONSE_TOPIC")

		userID, err := uuuid.NewV4()
		Expect(err).ToNot(HaveOccurred())

		mockUser = &user.User{
			UserID:    userID,
			FirstName: "test-first",
			LastName:  "test-last",
			Email:     "test-email",
			// Can only insert unique-usernames
			UserName: "test-username" + userID.String(),
			Password: "test-password",
			Role:     "test-user",
		}
		marshalUser, err := json.Marshal(mockUser)
		Expect(err).ToNot(HaveOccurred())

		cid, err := uuuid.NewV4()
		Expect(err).ToNot(HaveOccurred())
		uid, err := uuuid.NewV4()
		Expect(err).ToNot(HaveOccurred())
		timeUUID, err := uuuid.NewV1()
		Expect(err).ToNot(HaveOccurred())
		mockEvent = &model.Event{
			Action:        "insert",
			CorrelationID: cid,
			AggregateID:   user.AggregateID,
			Data:          marshalUser,
			Timestamp:     time.Now(),
			UserUUID:      uid,
			TimeUUID:      timeUUID,
			Version:       0,
			YearBucket:    2018,
		}
	})

	Describe("User Operations", func() {
		It("should query record", func(done Done) {
			Byf("Producing MockEvent")
			p, err := kafka.NewProducer(&kafka.ProducerConfig{
				KafkaBrokers: kafkaBrokers,
			})
			Expect(err).ToNot(HaveOccurred())
			marshalEvent, err := json.Marshal(mockEvent)
			Expect(err).ToNot(HaveOccurred())
			p.Input() <- kafka.CreateMessage(eventsTopic, marshalEvent)

			Byf("Creating query args")
			queryArgs := map[string]interface{}{
				"userID": mockUser.UserID,
			}
			marshalQuery, err := json.Marshal(queryArgs)
			Expect(err).ToNot(HaveOccurred())

			Byf("Creating query MockEvent")
			timeUUID, err := uuuid.NewV1()
			Expect(err).ToNot(HaveOccurred())
			mockEvent.Action = "query"
			mockEvent.Data = marshalQuery
			mockEvent.Timestamp = time.Now()
			mockEvent.TimeUUID = timeUUID

			Byf("Producing MockEvent")
			p, err = kafka.NewProducer(&kafka.ProducerConfig{
				KafkaBrokers: kafkaBrokers,
			})
			Expect(err).ToNot(HaveOccurred())
			marshalEvent, err = json.Marshal(mockEvent)
			Expect(err).ToNot(HaveOccurred())
			p.Input() <- kafka.CreateMessage(eventsTopic, marshalEvent)

			// Check if MockEvent was processed correctly
			Byf("Consuming Result")
			c, err := kafka.NewConsumer(&kafka.ConsumerConfig{
				KafkaBrokers: kafkaBrokers,
				GroupName:    "agguser.test.group.1",
				Topics:       []string{producerResponseTopic},
			})
			msgCallback := func(msg *sarama.ConsumerMessage) bool {
				defer GinkgoRecover()
				kr := &model.KafkaResponse{}
				err := json.Unmarshal(msg.Value, kr)
				Expect(err).ToNot(HaveOccurred())

				if kr.UUID == mockEvent.TimeUUID {
					Expect(kr.Error).To(BeEmpty())
					Expect(kr.ErrorCode).To(BeZero())
					Expect(kr.CorrelationID).To(Equal(mockEvent.CorrelationID))
					Expect(kr.UUID).To(Equal(mockEvent.TimeUUID))

					result := &user.User{}
					err = json.Unmarshal(kr.Result, result)
					Expect(err).ToNot(HaveOccurred())

					Expect(result.UserID).To(Equal(mockUser.UserID))
					mockUser.ID = result.ID
					Expect(result).To(Equal(mockUser))
					return true
				}
				return false
			}

			handler := &msgHandler{msgCallback}
			c.Consume(context.Background(), handler)

			close(done)
		}, 20)
	})
})
