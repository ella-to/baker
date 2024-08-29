# Run the example

Firs, go to the root of the baker project and run the following command to start the baker

```
docker-compose up
```

Second, navigate to `examples/websocket` folder and run the following command

```
docker-compose up --build
```

There should be some logs prints out in Baker terminal indicating that services are registering

Finally, open postman and select file>new and select websocket. Then in URL section type `ws://localhost` and in header section add `HOST` as a key and `example.com` as value, now click connect. By now you should be able to connect to running websocket server behind proxy
