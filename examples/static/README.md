# Run the example

Firs, go to the root of the baker project and run the following command to start the baker

```
docker-compose up
```

Second, navigate to `examples/static` folder and run the following command

```
docker-compose up --build --scale example-service=3
```

There should be some logs prints out in Baker terminal indicating that services are registering

Finally, run the following curl command which demonstrate the dynamic reverse proxy of Baker

```
curl -H "Host: example.com" http://localhost:80/api/v1
```
