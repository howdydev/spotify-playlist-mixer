package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zmb3/spotify/v2"
	spotifyauth "github.com/zmb3/spotify/v2/auth"
	"golang.org/x/oauth2"
)

type Config struct {
	Spotify struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	} `json:"spotify"`
}

var (
	config Config
)

func init() {
	configFile, err := os.Open("config.json")
	if err != nil {
		log.Fatal("Error loading config file:", err)
	}
	defer configFile.Close()

	decoder := json.NewDecoder(configFile)
	if err := decoder.Decode(&config); err != nil {
		log.Fatal("Error decoding config file:", err)
	}
}

var ch = make(chan *oauth2.Token, 1)

func main() {
	var rootCmd = &cobra.Command{
		Use:   "spotify-playlist-mixer",
		Short: "A simple tool to mix playlists",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			var auth = spotifyauth.New(spotifyauth.WithRedirectURL("http://localhost:8888/callback"),
				spotifyauth.WithScopes(spotifyauth.ScopeUserReadPrivate, spotifyauth.ScopePlaylistReadPrivate,
					spotifyauth.ScopePlaylistModifyPublic, spotifyauth.ScopePlaylistModifyPrivate),
				spotifyauth.WithClientID(config.Spotify.ClientID),
				spotifyauth.WithClientSecret(config.Spotify.ClientSecret),
			)

			// Start a local server to handle the callback
			http.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
				token, err := auth.Token(ctx, r.URL.Query().Get("state"), r)
				if err != nil {
					http.Error(w, "Couldn't get token", http.StatusForbidden)
					log.Fatal(err)
				}
				if st := r.FormValue("state"); st != "state-string" {
					http.NotFound(w, r)
					log.Fatalf("State mismatch: %s != %s\n", st, "state-string")
				}
				ch <- token
				fmt.Fprintf(w, "Login Completed!")
			})
			go http.ListenAndServe(":8888", nil)

			// Redirect user to authorize the app
			url := auth.AuthURL("state-string")
			fmt.Println("Please log in to Spotify by visiting the following page in your browser:", url)

			// Wait for the authorization token
			token := <-ch

			// Create a new Spotify client using the obtained token
			client := spotify.New(auth.Client(ctx, token))

			user, err := client.CurrentUser(ctx)
			if err != nil {
				log.Fatal(err)
			}

			fmt.Println("Hello,", user.DisplayName)

			playlists, err := client.GetPlaylistsForUser(ctx, user.ID)
			if err != nil {
				log.Fatal(err)
			}
			fmt.Printf("\nHere are your playlists! You have %d playlists:\n", len(playlists.Playlists))
			for index, playlist := range playlists.Playlists {
				fmt.Printf("\n%d) %s (%d tracks)", index, playlist.Name, playlist.Tracks.Total)
			}

			fmt.Println("\nSelect playlists to mix (comma-separeted numbers):")
			var selectedIndicedStr string
			fmt.Scanln(&selectedIndicedStr)

			selectedIndicesSplit := strings.Split(selectedIndicedStr, ",")
			var selectedIndices []int

			fmt.Println("\nSelected playlists:")

			for _, indexStr := range selectedIndicesSplit {
				index, err := strconv.Atoi(strings.TrimSpace(indexStr))
				if err != nil {
					return fmt.Errorf("invalid index: %s", indexStr)
				}
				selectedIndices = append(selectedIndices, index)

				if index < 0 || index > len(playlists.Playlists)-1 {
					return fmt.Errorf("invalid index: %d", index)
				}

				playlist := playlists.Playlists[index]
				fmt.Printf("\n - %s (ID: %s)", playlist.Name, playlist.ID)
			}

			// Prompt user for a name for the new playlist
			var newPlaylistName string
			fmt.Println("\nEnter a name for the new playlist:")
			fmt.Scanln(&newPlaylistName)

			// Create a new playlist with the selected tracks
			newPlaylist, err := client.CreatePlaylistForUser(ctx, user.ID, newPlaylistName, "Playlist mixed by https://github.com/howdydev/spotify-playlist-mixer", false, false)
			if err != nil {
				log.Fatal(err)
			}

			var allTracks []spotify.ID

			for _, index := range selectedIndices {
				playlist := playlists.Playlists[index]
				offset := 0
				limit := 100
				for {
					items, err := client.GetPlaylistItems(ctx, playlist.ID, spotify.Offset(offset), spotify.Limit(limit))
					if err != nil {
						log.Fatal(err)
					}

					for _, item := range items.Items {
						if item.Track.Track != nil {
							allTracks = append(allTracks, item.Track.Track.ID)
						}
					}

					if len(items.Items) < limit {
						break
					}

					offset += limit
				}
			}

			// Shuffle the tracks
			rand.Shuffle(len(allTracks), func(i, j int) { allTracks[i], allTracks[j] = allTracks[j], allTracks[i] })

			fmt.Printf("\nMixing %d tracks...\n", len(allTracks))

			batchSize := 20
			var successfulAdditions int
			for i := 0; i < len(allTracks); i += batchSize {

				end := i + batchSize
				if end > len(allTracks) {
					end = len(allTracks)
				}

				tracks := allTracks[i:end]

				fmt.Printf("\nAdding tracks %d-%d to playlist...", i, end)

				_, err := client.AddTracksToPlaylist(ctx, newPlaylist.ID, tracks...)
				if err != nil {
					fmt.Printf("Error adding tracks to playlist: %s\n", err)
				}
				successfulAdditions++
			}

			fmt.Printf("\nSuccessfully created your new playlist mix: %s\n", newPlaylistName)

			// exit application
			os.Exit(0)

			return nil
		},
	}

	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
