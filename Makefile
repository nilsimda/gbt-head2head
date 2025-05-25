# run templ generation in watch mode to detect all .templ files and 
# re-create _templ.txt files on change, then send reload event to browser. 
# Default url: http://localhost:7331
live/templ:
	templ generate --watch --proxy="http://localhost:8080" --open-browser=false -v

# run air to detect any go file changes to re-build and re-run the server
live/server:
	air \
		--build.cmd "go build -o tmp/bin/main" --build.bin "tmp/bin/main" --build.delay "100" \
		--build.include_ext "go" \
		--build.stop_on_error "true" \
		--misc.clean_on_exit "true"

# run tailwindcss to generate the styles.css file bundle in watch mode
live/tailwind:
	./tailwindcss -i assets/css/input.css -o assets/css/styles.css --minify --watch

# watch for any js or css change in the assets/ folder, then reload the browser via templ proxy
live/sync_assets:
	air \
		--build.cmd "templ generate --notify-proxy" \
		--build.bin "true" \
		--build.delay "100" \
		--build.exclude_dir "" \
		--build.include_dir "assets" \
		--build.include_ext "js,css"

# start all 4 watch processes in parallel
live:
	make -j4 live/templ live/server live/tailwind live/sync_assets


