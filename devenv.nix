{ pkgs, lib, config, ... }:

{
  # https://devenv.sh/basics/
  env.STELLA_CONFIG_PATH = "./config.yaml";

  # https://devenv.sh/packages/
  packages = [
    pkgs.tailwindcss # standalone CLI — compiles styles/input.css -> public/css/app.css
    pkgs.sqlite      # headers/lib for the CGO-enabled mattn/go-sqlite3 build
  ];

  # https://devenv.sh/languages/
  languages.go.enable = true;

  # https://devenv.sh/scripts/
  scripts.build-css.exec = ''
    tailwindcss -c tailwind.config.js -i styles/input.css -o public/css/app.css --minify
  '';

  # https://devenv.sh/processes/
  # Run with: devenv up
  processes.stella.exec = "go run ./src/main.go";

  # https://devenv.sh/basics/
  enterShell = ''
    build-css
  '';
}
