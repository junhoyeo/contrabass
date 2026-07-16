{
  description = "Contrabass — A project-level orchestrator for AI coding agents";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};

        contrabass = pkgs.buildGoModule {
          pname = "contrabass";
          version = self.shortRev or "dirty";
          src = ./.;

          # Empty string disables vendor hash checking
          vendorHash = "sha256-9Ky5onabMw3PFjjtmHZzD+65YDxSkQlVwKoQa/V+zw0=";

          env = {
            CGO_ENABLED = "0";
          };

          subPackages = [ "cmd/contrabass" ];

          ldflags = [
            "-s"
            "-w"
            "-X main.version=${version}"
            "-X main.commit=${self.shortRev or "dirty"}"
          ];

          meta = with pkgs.lib; {
            description = "A project-level orchestrator for AI coding agents";
            homepage = "https://github.com/junhoyeo/contrabass";
            license = licenses.asl20;
            maintainers = [ ];
            mainProgram = "contrabass";
          };
        };
      in
      {
        packages = {
          default = contrabass;
          contrabass = contrabass;
        };

        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            go
            gopls
            gotools
            go-tools
            goreleaser
            bun
          ];
        };
      }
    );
}
