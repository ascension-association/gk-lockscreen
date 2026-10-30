# lockscreen
Lock screen for gokrazy. A simple alternative to [fbstatus](https://github.com/gokrazy/fbstatus) if your device is headless or you want to reduce memory usage.

### Usage

```
gok add github.com/ascension-association/gk-lockscreen
gok update
```

### Display text

By default the screen shows "LOCKED" in the center, and replaces it with the device IP address once the network is up (and again if the address changes). To show your own message instead, or pass `-text=` for a blank screen, set the `-text` flag in your gokrazy `config.json`:

```json
"PackageConfig": {
    "github.com/ascension-association/gk-lockscreen": {
        "CommandLineFlags": ["-text=Running"]
    }
}
```
