"""Prove the optional profile preserves resolved Compose configuration; start nothing."""

import json
import os
from pathlib import Path
import subprocess
import tempfile

root = Path(__file__).resolve().parents[1]
with tempfile.TemporaryDirectory(prefix="kydrive-profile-check-") as directory:
    work = Path(directory)
    fixture = work / ".env"
    fixture.write_text(
        "KY_BULK_PATH=/srv/kydrive-bulk\n"
        "KY_DNS=192.0.2.53\n"
        "KY_CONTAINER_IP=192.0.2.10\n"
        "KY_NETWORK_SUBNET=192.0.2.0/24\n"
        "KY_OFFICE_JWT_FILE=/private/editor-jwt\n"
    )
    (work / ".env.runtime").write_text(
        "KY_SESSION_SECRET=" + "ab" * 32 + "\n"
        "KY_ENCRYPTION_KEY=" + "cd" * 32 + "\n"
        "KY_KYSIGNON_HMAC_SECRET=" + "ef" * 16 + "\n"
    )
    env = {key: value for key, value in os.environ.items() if not key.startswith(("COMPOSE_", "KY_", "KYDRIVE_", "GODEBUG"))}

    def render(files):
        command = ["docker", "compose", "--project-name", "kydrive-profile-check", "--project-directory", directory, "--env-file", str(fixture)]
        for file in files:
            command += ["-f", str(root / file)]
        return json.loads(subprocess.check_output(command + ["config", "--format", "json"], env=env))

    overlays = ["docker-compose.build.yml", "docker-compose.lan-dns.yml", "docker-compose.static-ip.yml", "docker-compose.office.yml"]
    chains = [[], *[[overlay] for overlay in overlays], overlays]
    for chain in chains:
        files = ["docker-compose.yml", *chain]
        baseline = render(files)
        configured = render([*files, "docker-compose.strong-crypto.yml"])
        assert configured["services"]["app"]["environment"].pop("GODEBUG") == "fips140=on"
        assert configured == baseline, f"The strong profile changed another Compose setting in {files}"
print(f"All {len(chains)} Compose chains keep data, keys, image/build, network, DNS and editor configuration.")
