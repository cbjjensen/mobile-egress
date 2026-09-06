The certificate and matching private key here are **public, disposable test fixtures** for localhost only. They must never be used for a deployed service or installed into a system trust store. The automated test supplies this certificate as its Node client's explicit CA, with normal certificate and hostname verification enabled. Tests need no global OpenSSL installation.

Generated using OpenSSL for localhost and 127.0.0.1 with a ten-year validity; replace them together before September 2036. The key has no production identity or authority.
