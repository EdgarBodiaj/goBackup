package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/mux"
	libDatabox "github.com/me-box/lib-go-databox"
	"github.com/zmb3/spotify"
)

//default addresses to be used in testing mode
const testArbiterEndpoint = "tcp://127.0.0.1:4444"
const testStoreEndpoint = "tcp://127.0.0.1:5555"

//redirect address for spotify oauth
const redirectURI = "https://127.0.0.1/spotify-history-driver/ui/callback"
//const redirectURI = "https://127.0.0.1/core-ui/ui/view/spotify-history-driver/callback"

var (
	auth  = spotify.NewAuthenticator(redirectURI, spotify.ScopeUserReadPrivate, spotify.ScopeUserReadRecentlyPlayed)
	state = "abc123"
)

func main() {
	//Set client_id and client_secret for the application inside the auth object
	auth.SetAuthInfo("2706f5aa27b646d8835a6a8aca7eba37", "eb8aec62450e4d44a4308f07b82338cb")
	libDatabox.Info("Starting ....")

	router := mux.NewRouter()
	router.HandleFunc("/status", statusEndpoint).Methods("GET")
	router.HandleFunc("/ui/callback", completeAuth)
	router.HandleFunc("/ui/auth",authHandle)
	router.HandleFunc("/ui", startAuth)
	setUpWebServer(false, router, "8080")
}

func statusEndpoint(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("active\n"))
}

func completeAuth(w http.ResponseWriter, r *http.Request) {
	libDatabox.Info("Callback handle")
	tok, err := auth.Token(state, r)
	if err != nil {
		http.Error(w, "Could not get token", http.StatusForbidden)
		fmt.Println("Error ", err)
		return
	}
	if st := r.FormValue("state"); st != state {
		http.NotFound(w, r)
		fmt.Println("State mismatch: %s != %s\n", st, state)
			return
	}

	fmt.Fprintf(w, "<h1>Authenticated</h1>")

	client := auth.NewClient(tok)

	go startDriverWork(client)

}
func authHandle(w http.ResponseWriter, r *http.Request) {
	url := auth.AuthURL(state)
	libDatabox.Info("Auth handle")
	fmt.Fprintf(w, "<script>window.parent.postMessage({ type:'databox_oauth_redirect', url: '%s'}, '*');</script>", url)
}


func startAuth(w http.ResponseWriter, r *http.Request) {
	//Display authentication page
	//url := auth.AuthURL(state)
	fmt.Fprintf(w, "<h1>Authenticate</h1>")
	fmt.Fprintf(w, "<title>Authentication Page</title>")
	//fmt.Fprintf(w, "<a href='%s'>Press to authenticate</a>", url)
	fmt.Fprintf(w, "<a href='/spotify-history-driver/ui/auth'>Press to authenticate</a>")
}

func setUpWebServer(testMode bool, r *mux.Router, port string) {

	//Start up a well behaved HTTP/S server for displying the UI

	srv := &http.Server{
		Addr:         ":" + port,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  30 * time.Second,
		Handler:      r,
	}
	if testMode {
		//set up an http server for testing
		libDatabox.Info("Waiting for http requests on port http://127.0.0.1" + srv.Addr)
		log.Fatal(srv.ListenAndServe())
	} else {
		//configure tls
		tlsConfig := &tls.Config{
			PreferServerCipherSuites: true,
			CurvePreferences: []tls.CurveID{
				tls.CurveP256,
			},
		}

		srv.TLSConfig = tlsConfig

		libDatabox.Info("Waiting for https requests on port " + srv.Addr)
		log.Fatal(srv.ListenAndServeTLS(libDatabox.GetHttpsCredentials(), libDatabox.GetHttpsCredentials()))
	}
}

func startDriverWork(client spotify.Client) {
	DataboxTestMode := os.Getenv("DATABOX_VERSION") == ""

	// Read in the store endpoint provided by databox
	// this is a driver so you will get a core-store
	// and you are responsible for registering datasources
	// and writing in data.
	var DataboxStoreEndpoint string
	var storeClient *libDatabox.CoreStoreClient
	if DataboxTestMode {
		DataboxStoreEndpoint = testStoreEndpoint
		ac, _ := libDatabox.NewArbiterClient("./", "./", testArbiterEndpoint)
		storeClient = libDatabox.NewCoreStoreClient(ac, "./", DataboxStoreEndpoint, false)
		//turn on debug output for the databox library
		libDatabox.OutputDebug(true)
	} else {
		DataboxStoreEndpoint = os.Getenv("DATABOX_STORE_ENDPOINT")
		storeClient = libDatabox.NewDefaultCoreStoreClient(DataboxStoreEndpoint)
	}

	libDatabox.Info("starting driver work")

	//register our datasources
	//we only need to do this once at start up
	testDatasource := libDatabox.DataSourceMetadata{
		Description:    "Spotify Playlist Data",    //required
		ContentType:    libDatabox.ContentTypeJSON, //required
		Vendor:         "databox-test",             //required
		DataSourceType: "playlistData",             //required
		DataSourceID:   "SpotifyData",              //required
		StoreType:      libDatabox.StoreTypeTSBlob, //required
		IsActuator:     false,
		IsFunc:         false,
	}
	arr := storeClient.RegisterDatasource(testDatasource)
	if arr != nil {
		libDatabox.Err("Error Registering Datasource " + arr.Error())
		return
	}
	libDatabox.Info("Registered Datasource")

	var recentTime int64
	var opts spotify.RecentlyPlayedOptions

	opts.Limit = 50
	opts.AfterEpochMs = recentTime

	for {
		results, err := client.PlayerRecentlyPlayedOpt(&opts)
		if err != nil {
			fmt.Println("Error ", err)
			return
		}
		if len(results) > 0 {
			//Get most recent items time and convernt to milliseconds
			recentTime = results[0].PlayedAt.Unix() * 1000
			fmt.Println(recentTime)
			opts.AfterEpochMs = recentTime + 500

			libDatabox.Info("Converting data")
			for i := len(results) - 1; i > -1; i-- {
				b, err := json.Marshal(results[i])
				if err != nil {
					fmt.Println("Error ", err)
					return
				}
				aerr := storeClient.TSBlobJSON.WriteAt(testDatasource.DataSourceID, results[i].PlayedAt.Unix()*1000, b)
				if aerr != nil {
					libDatabox.Err("Error Write Datasource " + aerr.Error())
				}
				libDatabox.Info("Data written to store: " + string(b))

			}
			libDatabox.Info("Storing data")
		} else {
			libDatabox.Info("No new data")
		}
		//time.Sleep(time.Hour * 2)
		fmt.Println(len(results))
		time.Sleep(time.Second * 10)
	}
}
