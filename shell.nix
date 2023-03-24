{ pkgs ? import <nixpkgs> {} }:

with pkgs;

mkShell {
  name="htshell";
  buildInputs = [
    go
    inotify-tools
  ];
}
