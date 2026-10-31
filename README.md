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

### Ramekin pairing code

On a [Ramekin](https://github.com/ascension-association/ramekin) device waiting to be claimed, the screen shows its pairing code ("Pairing code 768109") in place of the text or IP address. Ramekin's agent writes the code to `/run/ramekin/pairing-code` and removes it once the device is claimed. To read another file, or to never show a code, set `-pairing-code-file=<path>` or `-pairing-code-file=` respectively.

### Rotation

For displays mounted in portrait (e.g. the HyperPixel 4.0, whose panel is natively 480x800), rotate the text clockwise with `-rotate=90`, `-rotate=180` or `-rotate=270`:

```json
"CommandLineFlags": ["-rotate=90"]
```

### Displays

The first `/dev/dri/card*` device with a connected display is used, so boards whose display controller is not `card0` (such as the Raspberry Pi 5) work too. Displays that need a device tree overlay (DPI/SPI panels like the HyperPixel) must be enabled in the boot config first. The display must be connected at boot.
