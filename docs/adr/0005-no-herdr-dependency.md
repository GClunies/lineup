# No dependency on herdr

lineup is a Go program that copies useful code from the user's `herdr-linear` fork (the Linear client, OAuth, cache, and Markdown view) into this repo. It never imports herdr code and never calls the `herdr` binary. We chose this because lineup runs on Rex only, and a copy lets the code change freely here.
