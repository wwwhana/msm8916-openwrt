#!/usr/bin/env python3
"""Build ModemConfig-KO without modifying the pinned module cache."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


def run(*args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


go, output = sys.argv[1:]
root = Path(__file__).resolve().parent
os.chdir(root)
# OpenWrt uses musl; a native glibc-linked Go executable will not start there.
os.environ["CGO_ENABLED"] = "0"
module = json.loads(subprocess.check_output(
    [go, "list", "-m", "-json", "github.com/damonto/ims-go"], text=True))
if module["Version"] != "v0.1.2":
    raise SystemExit("unsupported ims-go version")
source = Path(module["Dir"])
with tempfile.TemporaryDirectory(prefix="modemconfig-ko-build-", dir=Path(output).parent) as temporary:
    temporary = Path(temporary)
    view = temporary / "ims-go"
    patch_path = root / "ims-go-ko.patch"
    changed = {Path(line[6:]) for line in patch_path.read_text().splitlines()
               if line.startswith("+++ b/")}
    if not changed or any(p.is_absolute() or ".." in p.parts for p in changed):
        raise SystemExit("invalid patch targets")
    changed.add(Path("ims/voice/modemconfig_ko_failure_test.go"))

    def link_view(relative=Path()):
        target = view / relative
        target.mkdir()
        for original in (source / relative).iterdir():
            entry = relative / original.name
            destination = view / entry
            if entry in changed or entry == Path("go.mod"):
                shutil.copyfile(original, destination)
            elif any(entry in path.parents for path in changed):
                link_view(entry)
            else:
                destination.symlink_to(original)

    # Go disallows overlays inside GOMODCACHE. Keep the module in place and
    # link its unchanged files into a temporary view; only the patch targets
    # and module metadata need real copies.
    link_view()
    with patch_path.open("rb") as patch:
        run("patch", "--silent", "--fuzz=0", "-p1", "-d", str(view), stdin=patch)
    shutil.copyfile(root / "testdata/sms_contact_test.go", view / "ims/registration/modemconfig_ko_sms_contact_test.go")
    shutil.copyfile(root / "testdata/carrier_config_test.go", view / "modemconfig_ko_carrier_config_test.go")
    shutil.copyfile(root / "testdata/sip_transport_test.go", view / "ims/session/modemconfig_ko_transport_test.go")
    shutil.copyfile(root / "testdata/sip_response_test.go", view / "ims/sip/modemconfig_ko_response_test.go")
    shutil.copyfile(root / "testdata/voice_failure_test.go", view / "ims/voice/modemconfig_ko_failure_test.go")
    modfile = temporary / "modemconfig-ko.mod"
    shutil.copyfile(root / "go.mod", modfile)
    shutil.copyfile(root / "go.sum", temporary / "modemconfig-ko.sum")
    run(go, "mod", "edit", "-modfile", str(modfile), "-replace", f"github.com/damonto/ims-go={view}")
    run(go, "test", "-modfile", str(modfile), "-p", "4", "./...")
    run(go, "test", "-modfile", str(modfile), "-p", "4", "github.com/damonto/ims-go/ims/registration",
        "-v", "-run", "TestModemConfigKO")
    run(go, "test", "-modfile", str(modfile), "-p", "4", "github.com/damonto/ims-go/ims/registration")
    run(go, "test", "-modfile", str(modfile), "-p", "4", "github.com/damonto/ims-go/ims/sms")
    run(go, "test", "-modfile", str(modfile), "-p", "4", "github.com/damonto/ims-go/ims/session",
        "-run", "Test(ModemConfigKO|PrepareRequestTransport)")
    run(go, "test", "-modfile", str(modfile), "-p", "4", "github.com/damonto/ims-go/ims/sip", "-run", "TestModemConfigKO")
    run(go, "test", "-modfile", str(modfile), "-p", "4", "github.com/damonto/ims-go/ims/voice", "-run", "Test(ModemConfigKO|HangupCancelsOutgoingDialBeforeFinalResponse|DialHandlesRetransmittedInvite2xx|DialPreparesMediaBeforeInvite|IncomingInviteRingingResponseIncludesContact|IncomingCancelEndsRingingCall|IncomingReliableInviteAnswerGateSurvivesCancel|IncomingReliableInviteAnswerWaitsForPRACKBeforeRinging)")
    run(go, "test", "-modfile", str(modfile), "-p", "4", "github.com/damonto/ims-go",
        "-run", "Test.*(ModemConfigKO|Config|IMSPDNNetwork|QCOMIMSPDNNetworkInfo)")
    run(go, "build", "-modfile", str(modfile), "-p", "4", "-trimpath", "-ldflags=-s -w", "-o", output, ".")
    print("ModemConfig-KO tests and Linux/arm64 build passed", flush=True)
