package models

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Microsoft/ApplicationInsights-Go/appinsights"
	"github.com/streadway/amqp"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

// The order map
var (
	OrderList map[string]*Order
)

var (
	database string
	password string
	status   string
)

var username string
var address []string
var isAzure bool
var session *mgo.Session
var serr error

var hosts string
var db string

var insightskey = "23c6b1ec-ca92-4083-86b6-eba851af9032"

var rabbitMQURL = os.Getenv("RABBITMQHOST")
var partitionKey = strings.TrimSpace(os.Getenv("PARTITIONKEY"))
var mongoURL = os.Getenv("MONGOHOST")
var teamname = os.Getenv("TEAMNAME")

// Order represents the order json
type Order struct {
	ID                string  `required:"false" description:"CosmoDB ID - will be autogenerated"`
	EmailAddress      string  `required:"true" description:"Email address of the customer"`
	PreferredLanguage string  `required:"false" description:"Preferred Language of the customer"`
	Product           string  `required:"false" description:"Product ordered by the customer"`
	Total             float64 `required:"false" description:"Order total"`
	Source            string  `required:"false" description:"Source backend e.g. App Service, Container instance, K8 cluster etc"`
	Status            string  `required:"true" description:"Order Status"`
}

func init() {
	OrderList = make(map[string]*Order)
}

func AddOrder(order Order) (orderId string) {

	return orderId
}

// AddOrderToMongoDB Add the order to MondoDB
func AddOrderToMongoDB(order Order) (orderId string) {

	if partitionKey == "" {
		partitionKey = "0"
	}

	NewOrderID := bson.NewObjectId()

	order.ID = NewOrderID.Hex()

	order.Status = "Open"
	if order.Source == "" || order.Source == "string" {
		order.Source = os.Getenv("SOURCE")
	}

	database = "k8orders"
	password = "" //V2
	//isAzure = false // REMOVE this for V2 tag and for cosmos to work

	//Now we check if this mongo or cosmos // V2
	if strings.Contains(mongoURL, "?ssl=true") {
		isAzure = true

		url, err := url.Parse(mongoURL)
		if err != nil {
			log.Fatal("Problem parsing url: ", err)
		}

		log.Print("user ", url.User)
		// DialInfo holds options for establishing a session with a MongoDB cluster.
		st := fmt.Sprintf("%s", url.User)
		co := strings.Index(st, ":")

		database = st[:co]
		password = st[co+1:]
		log.Print("db ", database, " pwd ", password)
	}
	// V2s

	log.Print(mongoURL, isAzure)

	dialInfo := &mgo.DialInfo{
		Addrs:    []string{fmt.Sprintf("%s.documents.azure.com:10255", database)}, // Get HOST + PORT
		Timeout:  60 * time.Second,
		Database: database, // It can be anything
		Username: database, // Username
		Password: password, // PASSWORD
		DialServer: func(addr *mgo.ServerAddr) (net.Conn, error) {
			return tls.Dial("tcp", addr.String(), &tls.Config{})
		},
	}
	//V2
	// Create a session which maintains a pool of socket connections
	// to our MongoDB.
	if isAzure == true {
		session, serr = mgo.DialWithInfo(dialInfo)
		log.Println("Writing to CosmosDB")
		db = "CosmosDB"
	} else {
		session, serr = mgo.Dial(mongoURL)
		log.Println("Writing to MongoDB")
		db = "MongoDB"
	}

	if serr != nil {
		log.Fatal("Can't connect to mongo, go error", serr)
		status = "Can't connect to mongo, go error %v\n"
		os.Exit(1)
	}

	defer session.Close()

	// SetSafe changes the session safety mode.
	// If the safe parameter is nil, the session is put in unsafe mode, and writes become fire-and-forget,
	// without error checking. The unsafe mode is faster since operations won't hold on waiting for a confirmation.
	// http://godoc.org/labix.org/v2/mgo#Session.SetMode.
	session.SetSafe(&mgo.Safe{})

	// get collection
	collection := session.DB(database).C("orders")

	// insert Document in collection
	serr = collection.Insert(order)
	log.Println("_id:", order)

	if serr != nil {
		log.Fatal("Problem inserting data: ", serr)
		status = "CProblem inserting data, go error %v\n"
		return ""
	}

	//	Let's write only if we have a key
	if insightskey != "" {
		t := time.Now()
		client := appinsights.NewTelemetryClient(insightskey)
		client.TrackEvent("Team Name " + teamname + " db " + db)
		client.TrackTrace(t.String())
	}

	// Let's send to RabbitMQ
	//	AddOrderToRabbitMQ(order.ID, order.Source)

	// Now let's place this on the eventhub
	/* if eventURL != "" {
		AddOrderToEventHub(order.ID, order.Source)
	} */

	return order.ID
}

// AddOrder to RabbitMQ

func AddOrderToRabbitMQ(orderId string, orderSource string) {

	log.Println("Rabbit")

	conn, err := amqp.Dial(rabbitMQURL)
	failOnError(err, "Failed to connect to RabbitMQ")
	defer conn.Close()

	ch, err := conn.Channel()
	failOnError(err, "Failed to open a channel")
	defer ch.Close()

	q, err := ch.QueueDeclare(
		"order"+partitionKey, // name
		false, // durable
		false, // delete when unused
		false, // exclusive
		false, // no-wait
		nil,   // arguments
	)
	failOnError(err, "Failed to declare a queue")

	body := "{'order':" + "'" + orderId + "', 'source':" + "'" + orderSource + "'}"
	err = ch.Publish(
		"",     // exchange
		q.Name, // routing key
		false,  // mandatory
		false,  // immediate
		amqp.Publishing{
			ContentType: "text/plain",
			Body:        []byte(body),
		})
	log.Printf(" [x] Sent %s", body)
	failOnError(err, "Failed to publish a message")
}

func failOnError(err error, msg string) {
	if err != nil {
		log.Fatalf("%s: %s", msg, err)
		panic(fmt.Sprintf("%s: %s", msg, err))
	}
}
