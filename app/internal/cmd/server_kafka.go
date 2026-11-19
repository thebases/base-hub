package cmd

// File overview:
// - Responsibility: Kafka producer/consumer startup and broker status logging for the server.
// - Key symbols: `startKafkaProducerIfEnabled`, `startKafkaConsumersIfEnabled`, `logKafkaBrokerStatus`.

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	kafkaConsumer "git.thebase.vn/base_hub/internal/services/kafka/consumer"
	kafkaproducer "git.thebase.vn/base_hub/internal/services/kafka/producer"
	"git.thebase.vn/base_hub/internal/services/log"
)

func startKafkaProducerIfEnabled() *kafkaproducer.Producer {
	if !config.Get().Kafka.Enable {
		return nil
	}
	log.Infof("===== Kafka is enabled =====")
	if config.Get().Kafka.IsProducer {
		logKafkaBrokerStatus("producer", config.Get().Kafka.Brokers)
		log.Infof("===== Use Kafka Producer =====")
		return kafkaproducer.NewProducer(config.Get().Kafka.Brokers)
	}
	return nil
}

func startKafkaConsumersIfEnabled(ctx context.Context) (context.CancelFunc, *sync.WaitGroup) {
	if !config.Get().Kafka.Enable {
		return nil, nil
	}
	brokers := config.Get().Kafka.Brokers
	topics := config.Get().Kafka.Topics
	log.Debugf(`brokers: %s - topics: %s`, brokers, topics)
	if !config.Get().Kafka.IsComsumer {
		return nil, nil
	}
	logKafkaBrokerStatus("consumer", brokers)
	log.Infof("===== Use Kafka Consumer =====")
	opts := kafkaConsumer.DefaultConsumerOptions()
	if config.Get().AppEnv() == "production" {
		opts.GroupID = "prod-ipn-consumer-group"
	} else {
		opts.GroupID = "dev-ipn-consumer-group"
	}
	opts.DedupeTTL = 5 * time.Minute
	opts.DedupeMaxItems = 100_000
	opts.PrettyJSON = true
	opts.MaxLogLen = 4096
	return kafkaConsumer.StartConsumersPerTopic(ctx, brokers, topics, kafkaConsumer.KafkaHandlers(), opts)
}

func logKafkaBrokerStatus(role string, brokers []string) {
	if len(brokers) == 0 {
		log.Warnf("kafka: %s startup skipped broker probe because no brokers are configured", role)
		return
	}
	// Probe each configured broker once during bootstrap so operators can see
	// reachability before producer or consumer traffic starts flowing.
	for _, broker := range brokers {
		broker = strings.TrimSpace(broker)
		if broker == "" {
			continue
		}
		log.Infof("kafka: %s connecting to broker=%s", role, broker)
		conn, err := net.DialTimeout("tcp", broker, 3*time.Second)
		if err != nil {
			log.Errorf("kafka: %s failed to connect broker=%s: %v", role, broker, err)
			continue
		}
		if err := conn.Close(); err != nil {
			log.Warnf("kafka: %s connected to broker=%s but close returned: %v", role, broker, err)
			continue
		}
		log.Infof("kafka: %s connected to broker=%s", role, broker)
	}
}
