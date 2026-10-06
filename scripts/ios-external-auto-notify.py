"""Prepare one explicitly selected external TestFlight release; no invitation calls."""
import base64
from datetime import datetime, timezone
import json
import re
import subprocess
import time
import urllib.error
import urllib.request


class SafeError(Exception):
    pass


def require(condition, message="Apple release preflight did not match the explicit selection."):
    if not condition:
        raise SafeError(message)


def release_number(value):
    require(isinstance(value, str) and re.fullmatch(r"(0|[1-9]\d{0,8})(\.(0|[1-9]\d{0,8})){0,2}", value))
    parts = tuple(int(part) for part in value.split("."))
    return parts + (0,) * (3 - len(parts))


def relation(resource, name, kind):
    value = resource.get("relationships", {}).get(name, {}).get("data")
    require(isinstance(value, dict) and value.get("type") == kind and re.fullmatch(r"[A-Za-z0-9-]{1,100}", value.get("id", "")))
    return value["id"]


def prepare_auto_notify(request, selection, apply=False):
    require(all(isinstance(selection.get(key), str) and re.fullmatch(r"[A-Za-z0-9-]{1,100}", selection[key]) for key in ("appId", "groupId", "buildId")))
    version, number = release_number(selection.get("version")), release_number(selection.get("buildNumber"))
    require(version[0] == 2 and (version, number) >= ((2, 0, 0), (7, 0, 0)), "Release must meet the compatible 2.0.0 (7) floor and remain in 2.x.")
    group = request("GET", "betaGroups/" + selection["groupId"] + "?include=app")["data"]
    require(group.get("type") == "betaGroups" and group.get("id") == selection["groupId"] and group.get("attributes", {}).get("isInternalGroup") is False)
    require(relation(group, "app", "apps") == selection["appId"])
    document = request("GET", "builds/" + selection["buildId"] + "?include=app,preReleaseVersion,buildBetaDetail")
    build = document["data"]
    require(build.get("type") == "builds" and build.get("id") == selection["buildId"] and relation(build, "app", "apps") == selection["appId"])
    attrs = build.get("attributes", {})
    require(attrs.get("version") == selection["buildNumber"] and attrs.get("processingState") == "VALID" and attrs.get("buildAudienceType") == "APP_STORE_ELIGIBLE" and attrs.get("expired") is False)
    try:
        expires = datetime.fromisoformat(attrs.get("expirationDate", "").replace("Z", "+00:00"))
        require(expires > datetime.now(timezone.utc))
    except (TypeError, ValueError):
        raise SafeError("Build expiration could not be verified.") from None

    def included(name, kind):
        identity = relation(build, name, kind)
        matches = [item for item in document.get("included", []) if item.get("type") == kind and item.get("id") == identity]
        require(len(matches) == 1)
        return matches[0]

    prerelease = included("preReleaseVersion", "preReleaseVersions").get("attributes", {})
    require(prerelease.get("version") == selection["version"] and prerelease.get("platform") == "IOS")
    detail = included("buildBetaDetail", "buildBetaDetails")
    enabled = detail.get("attributes", {}).get("autoNotifyEnabled")
    state = detail.get("attributes", {}).get("externalBuildState")
    require(isinstance(enabled, bool) and isinstance(state, str))
    result = {**selection, "externalBuildState": state, "verified": enabled, "changeRequired": not enabled, "changed": False}
    if enabled or not apply:
        return result
    require(state == "READY_FOR_BETA_SUBMISSION", "Automatic notification must be prepared before beta review/testing; no late activation was attempted.")
    identity = detail["id"]
    request("PATCH", "buildBetaDetails/" + identity, {"data": {"type": "buildBetaDetails", "id": identity, "attributes": {"autoNotifyEnabled": True}}})
    verified = request("GET", "buildBetaDetails/" + identity)["data"]
    require(verified.get("type") == "buildBetaDetails" and verified.get("id") == identity and verified.get("attributes", {}).get("autoNotifyEnabled") is True, "Automatic notification readback failed; inspect Apple state before retrying.")
    return {**result, "verified": True, "changeRequired": False, "changed": True}


def token(config):
    def b64(value):
        return base64.urlsafe_b64encode(value).decode().rstrip("=")
    now = int(time.time())
    message = b64(json.dumps({"alg": "ES256", "kid": config["keyId"], "typ": "JWT"}).encode()) + "." + b64(json.dumps({"iss": config["issuer"], "iat": now - 5, "exp": now + 300, "aud": "appstoreconnect-v1"}).encode())
    signed = subprocess.run(["/usr/bin/openssl", "dgst", "-sha256", "-sign", config["keyPath"]], input=message.encode(), capture_output=True, timeout=10, check=True).stdout
    def take(data, offset):
        size, start = data[offset + 1], offset + 2
        if size & 128:
            count = size & 127
            size = int.from_bytes(data[start:start + count], "big")
            start += count
        return data[offset], data[start:start + size], start + size
    tag, seq, end = take(signed, 0)
    require(tag == 48 and end == len(signed), "Signing response was invalid.")
    rtag, r, pos = take(seq, 0)
    stag, s, end = take(seq, pos)
    require(rtag == 2 and stag == 2 and end == len(seq), "Signing response was invalid.")
    signature = int.from_bytes(r, "big").to_bytes(32, "big") + int.from_bytes(s, "big").to_bytes(32, "big")
    return message + "." + b64(signature)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def apple_request(bearer):
    opener = urllib.request.build_opener(NoRedirect())
    deadline = time.monotonic() + 60
    def request(method, path, payload=None):
        require(method in ("GET", "PATCH") and re.fullmatch(r"(?:betaGroups|builds|buildBetaDetails)/[A-Za-z0-9-]+(?:\?include=[A-Za-z,]+)?", path), "Unsupported Apple release operation.")
        remaining = deadline - time.monotonic()
        require(remaining > 0, "Apple release check deadline exceeded.")
        req = urllib.request.Request("https://api.appstoreconnect.apple.com/v1/" + path, method=method, data=json.dumps(payload).encode() if payload is not None else None, headers={"Authorization": "Bearer " + bearer, "Content-Type": "application/json"})
        try:
            with opener.open(req, timeout=min(15, remaining)) as response:
                body = response.read(1048577)
                require(len(body) <= 1048576, "Apple response exceeded the size limit.")
                return json.loads(body)
        except urllib.error.HTTPError as error:
            if method == "PATCH" and error.code >= 500:
                raise SafeError("Apple mutation outcome unknown; read state before retrying.") from None
            raise SafeError("Apple request rejected (HTTP " + str(error.code) + ").") from None
        except SafeError:
            raise
        except Exception:
            raise SafeError("Apple mutation outcome unknown; read state before retrying." if method == "PATCH" else "Apple read failed; no automatic retry attempted.") from None
    return request


def main(config):
    try:
        result = prepare_auto_notify(apple_request(token(config)), config["selection"], apply=config.get("apply") is True)
        print(json.dumps(result))
        return 0 if result["verified"] else 2
    except SafeError as error:
        print(json.dumps({"verified": False, "error": str(error)}))
        return 1
    except Exception:
        print(json.dumps({"verified": False, "error": "Release configuration or response was invalid; no automatic retry attempted."}))
        return 1


if __name__ == "__main__":
    raise SystemExit(main(globals().get("CONFIG", {})))
