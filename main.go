package main

import (
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"time"
)

var colors = []string{"#801B14", "#F2E4A4", "#A19D77", "#2A2B24", "#E0493F"}

const (
	KEY_ARROWNULL = iota
	KEY_ARROW_RIGHT
	KEY_ARROW_LEFT
	KEY_ARROW_DOWN
	KEY_ARROW_UP
)
const (
	ACTION_PING          = "p"
	ACTION_LOGIN         = "l"
	ACTION_LOGIN_SUCCESS = "ls"
	ACTION_LOGOUT        = "lg"
	ACTION_MOVE          = "m"
)

// client represents a single chatting user.
type client struct {
	// socket is the web socket for this client.
	socket *websocket.Conn
	// send is a channel on which messages are sent.
	send  chan []byte // room is the room this client is chatting in.
	room  *room
	name  string
	x     int
	y     int
	ping  int64
	color string
}
type room struct {
	// forward is a channel that holds incoming messages
	// that should be forwarded to the other clients.
	forward chan []byte
	// join is a channel for clients wishing to join the room.
	join chan *client
	// leave is a channel for clients wishing to leave the room.
	leave chan *client
	dm    chan dm
	// clients holds all current clients in this room.
	clients map[*client]bool
	players map[string]*client
	i       int
}
type dm struct {
	name    string
	message string
}

func (c *client) read() {
	defer c.socket.Close()
	for {
		_, msg, err := c.socket.ReadMessage()

		msgStr := string(msg)
		fmt.Println(fmt.Sprintf("[%s-%s] %s", c.name, time.Now().Format("2006-01-02 03:04:05"), msgStr))

		if err != nil {
			return
		}
		action := gjson.Get(msgStr, "a").Str
		switch action {
		case ACTION_PING:
			c.ping = time.Now().Unix()
		case ACTION_LOGIN:
			c.name = gjson.Get(msgStr, "n").Str
			c.room.players[c.name] = c
			c.room.dm <- dm{c.name, fmt.Sprintf(`{"a":"%s","p":"%s"}`, ACTION_LOGIN_SUCCESS, c.name)}

			for _, p := range c.room.players {
				c.room.dm <- dm{c.name, fmt.Sprintf(`{"a":"%s","p":"%s","x":%d,"y":%d,"c":"%s"}`, ACTION_LOGIN, p.name, p.x, p.y, p.color)}
			}

			c.room.forward <- []byte(fmt.Sprintf(`{"a":"%s","p":"%s","x":%d,"y":%d,"c":"%s"}`, ACTION_LOGIN, c.name, c.x, c.y, c.color))

		case ACTION_MOVE:
			target := int(gjson.Get(msgStr, "t").Num)
			if target == KEY_ARROW_RIGHT && c.x < 450 {
				c.x += 5
				c.room.forward <- []byte(fmt.Sprintf(`{"a":"%s","p":"%s","x":%d,"y":%d}`, ACTION_MOVE, c.name, c.x, c.y))
			}
			if target == KEY_ARROW_LEFT && c.x > 0 {
				c.x -= 5
				c.room.forward <- []byte(fmt.Sprintf(`{"a":"%s","p":"%s","x":%d,"y":%d}`, ACTION_MOVE, c.name, c.x, c.y))
			}
			if target == KEY_ARROW_DOWN && c.y < 450 {
				c.y += 5
				c.room.forward <- []byte(fmt.Sprintf(`{"a":"%s","p":"%s","x":%d,"y":%d}`, ACTION_MOVE, c.name, c.x, c.y))
			}
			if target == KEY_ARROW_UP && c.y > 0 {
				c.y -= 5
				c.room.forward <- []byte(fmt.Sprintf(`{"a":"%s","p":"%s","x":%d,"y":%d}`, ACTION_MOVE, c.name, c.x, c.y))
			}
		}

		/*
			message := fmt.Sprintf("%s:%s", string(c.name), string(msg))
			c.room.forward <- []byte(message)

		*/
	}

}
func (c *client) write() {
	defer c.socket.Close()
	for msg := range c.send {
		err := c.socket.WriteMessage(websocket.TextMessage, msg)
		if err != nil {
			return
		}
	}
}
func (r *room) checkClients() {
	for {
		time.Sleep(time.Second * 10)
		expireTime := time.Now().Unix() - 10
		for c := range r.clients {
			if c.ping < expireTime {
				r.leave <- c
			}
		}
	}
}
func (r *room) run() {
	go r.checkClients()
	for {
		select {
		case client := <-r.join:

			r.clients[client] = true
			client.color = colors[r.i%5]
			r.i++

			// joining
		case client := <-r.leave:
			// leaving
			for c := range r.clients {
				c.send <- []byte(fmt.Sprintf(`{"a":"%s","p":"%s"}`, ACTION_LOGOUT, client.name))
			}

			name := client.name
			delete(r.clients, client)
			delete(r.players, name)
			close(client.send)
		case msg := <-r.forward:
			// forward message to all clients
			for client := range r.clients {
				client.send <- msg
			}
		case dm := <-r.dm:
			// forward message to all clients
			if c, ok := r.players[dm.name]; ok {
				c.send <- []byte(dm.message)
			}
		}
	}
}

const (
	socketBufferSize  = 1024
	messageBufferSize = 256
)

var upgrader = &websocket.Upgrader{ReadBufferSize: socketBufferSize,
	WriteBufferSize: socketBufferSize}

func (r *room) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	upgrader.CheckOrigin = func(r *http.Request) bool { return true }
	socket, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		log.Fatal("ServeHTTP:", err)
		return
	}
	client := &client{
		socket: socket,
		send:   make(chan []byte, messageBufferSize),
		room:   r,
		ping:   time.Now().Unix(),
		x:      rand.Intn(350) + 50,
		y:      rand.Intn(350) + 50,
	}
	r.join <- client
	defer func() { r.leave <- client }()
	go client.write()
	client.read()
}
func newRoom() *room {
	return &room{
		forward: make(chan []byte),
		join:    make(chan *client),
		leave:   make(chan *client),
		dm:      make(chan dm),
		clients: make(map[*client]bool),
		players: make(map[string]*client),
	}
}
func rootHandler(w http.ResponseWriter, r *http.Request) {
	content, err := ioutil.ReadFile("index.html")
	if err != nil {
		fmt.Println("Could not open file.", err)
	}
	fmt.Fprintf(w, "%s", content)
}

func main() {
	r := newRoom()
	http.HandleFunc("/", rootHandler)
	http.Handle("/room", r)
	// get the room going
	go r.run()
	// start the web server
	if err := http.ListenAndServe(":9999", nil); err != nil {
		log.Fatal("ListenAndServe:", err)
	}
}
