package config

import (
	"encoding/json"
	"flag"
	"github.com/mailhog/MailHog-Server/monkey"
	"io/ioutil"
	"log"

	"github.com/ian-kent/envconf"
	"github.com/jay-dee7/storage"
	"github.com/mailhog/data"
)

// DefaultConfig is the default config
func DefaultConfig() *Config {
	return &Config{
		SMTPBindAddr: "0.0.0.0:1025",
		APIBindAddr:  "0.0.0.0:8025",
		Hostname:     "mailhog.test",
		MongoURI:     "127.0.0.1:27017",
		MongoDb:      "mailhog",
		MongoColl:    "messages",
		MaildirPath:  "",
		StorageType:  "memory",
		CORSOrigin:   "",
		WebPath:      "",
		MessageChan:  make(chan *data.Message),
		OutgoingSMTP: make(map[string]*OutgoingSMTP),
	}
}

// Config is the config, kind of
type Config struct {
	SMTPBindAddr     string
	APIBindAddr      string
	Hostname         string
	MongoURI         string
	MongoDb          string
	MongoColl        string
	StorageType      string
	CORSOrigin       string
	MaildirPath      string
	Storage          storage.MultiTenantStorage
	SimpleStorage    storage.Storage
	MessageChan      chan *data.Message
	Assets           func(asset string) ([]byte, error)
	OutgoingSMTPFile string
	OutgoingSMTP     map[string]*OutgoingSMTP
	WebPath          string
	InviteJim        bool
	Monkey           monkey.ChaosMonkey
}

// OutgoingSMTP is an outgoing SMTP server config
type OutgoingSMTP struct {
	Name      string
	Save      bool
	Email     string
	Host      string
	Port      string
	Username  string
	Password  string
	Mechanism string
}

var cfg = DefaultConfig()

// Configure configures stuff
func Configure(multiTenant bool) *Config {

	if multiTenant {
		s := storage.CreateMultiTenantMongoDB(cfg.MongoURI, cfg.MongoDb)
		cfg.Storage = s
	} else {
		s := storage.CreateMongoDB(cfg.MongoURI, cfg.MongoDb, cfg.MongoColl)
		cfg.SimpleStorage = s
	}

	if len(cfg.OutgoingSMTPFile) > 0 {
		b, err := ioutil.ReadFile(cfg.OutgoingSMTPFile)
		if err != nil {
			log.Fatal(err)
		}
		var o map[string]*OutgoingSMTP
		err = json.Unmarshal(b, &o)
		if err != nil {
			log.Fatal(err)
		}
		cfg.OutgoingSMTP = o
	}

	return cfg
}

// RegisterFlags registers flags
func RegisterFlags() {
	flag.StringVar(&cfg.SMTPBindAddr, "smtp-bind-addr", envconf.FromEnvP("MH_SMTP_BIND_ADDR", "0.0.0.0:1025").(string), "SMTP bind interface and port, e.g. 0.0.0.0:1025 or just :1025")
	flag.StringVar(&cfg.APIBindAddr, "api-bind-addr", envconf.FromEnvP("MH_API_BIND_ADDR", "0.0.0.0:8025").(string), "HTTP bind interface and port for API, e.g. 0.0.0.0:8025 or just :8025")
	flag.StringVar(&cfg.Hostname, "hostname", envconf.FromEnvP("MH_HOSTNAME", "mailhog.example").(string), "Hostname for EHLO/HELO response, e.g. mailhog.example")
	flag.StringVar(&cfg.StorageType, "storage", envconf.FromEnvP("MH_STORAGE", "memory").(string), "Message storage: 'memory' (default), 'mongodb' or 'maildir'")
	flag.StringVar(&cfg.MongoURI, "mongo-uri", envconf.FromEnvP("MH_MONGO_URI", "127.0.0.1:27017").(string), "MongoDB URI, e.g. 127.0.0.1:27017")
	flag.StringVar(&cfg.MongoDb, "mongo-db", envconf.FromEnvP("MH_MONGO_DB", "mailhog").(string), "MongoDB database, e.g. mailhog")
	flag.StringVar(&cfg.MongoColl, "mongo-coll", envconf.FromEnvP("MH_MONGO_COLLECTION", "messages").(string), "MongoDB collection, e.g. messages")
	flag.StringVar(&cfg.CORSOrigin, "cors-origin", envconf.FromEnvP("MH_CORS_ORIGIN", "").(string), "CORS Access-Control-Allow-Origin header for API endpoints")
	flag.StringVar(&cfg.MaildirPath, "maildir-path", envconf.FromEnvP("MH_MAILDIR_PATH", "").(string), "Maildir path (if storage type is 'maildir')")
	flag.StringVar(&cfg.OutgoingSMTPFile, "outgoing-smtp", envconf.FromEnvP("MH_OUTGOING_SMTP", "").(string), "JSON file containing outgoing SMTP servers")
}
