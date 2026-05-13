# Changelog

## [0.1.1](https://github.com/meigma/imgsrv/compare/v0.1.0...v0.1.1) (2026-05-13)


### Features

* add demo compose stack ([#56](https://github.com/meigma/imgsrv/issues/56)) ([46df21e](https://github.com/meigma/imgsrv/commit/46df21ef6be7e300a196415a0199490e58b17c29))
* add imgsrv service foundation ([#8](https://github.com/meigma/imgsrv/issues/8)) ([5d41e3d](https://github.com/meigma/imgsrv/commit/5d41e3d0b75dc92cd72d50172a8734f8052596ea))
* add observability foundation ([#9](https://github.com/meigma/imgsrv/issues/9)) ([89fad8d](https://github.com/meigma/imgsrv/commit/89fad8db8cec6dc464df0e3a60f9a05676a8cf3a))
* **api:** add abort upload endpoint ([#20](https://github.com/meigma/imgsrv/issues/20)) ([67e6b45](https://github.com/meigma/imgsrv/commit/67e6b4520396fd045608959d35492d00b68485a9))
* **api:** add CAS blob downloads and upload short-circuit ([#23](https://github.com/meigma/imgsrv/issues/23)) ([2e543c6](https://github.com/meigma/imgsrv/commit/2e543c66ea180fe77dd7b411bf74781a33702bc5))
* **api:** add draft release catalog flow ([#22](https://github.com/meigma/imgsrv/issues/22)) ([c07657c](https://github.com/meigma/imgsrv/commit/c07657cbf073908222a5195fac095b1fd76aa41c))
* **api:** add image alias API ([#24](https://github.com/meigma/imgsrv/issues/24)) ([2dd3131](https://github.com/meigma/imgsrv/commit/2dd3131c2957488b011ca0857bdbd0d120adb85d))
* **api:** add public catalog browse and draft deletes ([#25](https://github.com/meigma/imgsrv/issues/25)) ([3a772f6](https://github.com/meigma/imgsrv/commit/3a772f6700eece8c880f2e6c05baa8f1c4009575))
* **api:** add write auth and artifact downloads ([#26](https://github.com/meigma/imgsrv/issues/26)) ([f5f4fb7](https://github.com/meigma/imgsrv/commit/f5f4fb7daa3c6ad55ebf174c095b2b92eea1ca1b))
* **auth:** add action-based policy foundation ([#29](https://github.com/meigma/imgsrv/issues/29)) ([66bd6d1](https://github.com/meigma/imgsrv/commit/66bd6d11d3f51bbb3d1a7e0a7cdcb7abe43d8eec))
* **auth:** add admin auth management API ([#37](https://github.com/meigma/imgsrv/issues/37)) ([db33775](https://github.com/meigma/imgsrv/commit/db33775b628659bb0872bc6aa0ad8ba368c5d832))
* **auth:** add GitHub Actions OIDC publisher auth ([#32](https://github.com/meigma/imgsrv/issues/32)) ([6ef7f3b](https://github.com/meigma/imgsrv/commit/6ef7f3be1f588fecc8b8d39173fd275da9a254d2))
* **auth:** add OIDC JWT bearer authentication ([#31](https://github.com/meigma/imgsrv/issues/31)) ([f68f6fa](https://github.com/meigma/imgsrv/commit/f68f6fa936e9fda89226aaa58c97e9f9bedf21e5))
* **auth:** add OIDC provisioning rule reconciliation ([#39](https://github.com/meigma/imgsrv/issues/39)) ([92d0bcf](https://github.com/meigma/imgsrv/commit/92d0bcf601fbebc8985ae2d052f9fbda320ff105))
* **auth:** manage OIDC provisioning rules ([#36](https://github.com/meigma/imgsrv/issues/36)) ([bedf24c](https://github.com/meigma/imgsrv/commit/bedf24c63f442edea5a76e1584b5ef2ed76b049e))
* **cas:** implement staged upload commit ([#14](https://github.com/meigma/imgsrv/issues/14)) ([ebe72c3](https://github.com/meigma/imgsrv/commit/ebe72c35d2631b2c2eadf4505c2594fda821089e))
* **client:** add upload SDK ([#18](https://github.com/meigma/imgsrv/issues/18)) ([784ee5f](https://github.com/meigma/imgsrv/commit/784ee5f267666d218a70ec65170a44592f697f41))
* **httpapi:** add upload staging endpoints ([#15](https://github.com/meigma/imgsrv/issues/15)) ([3809c70](https://github.com/meigma/imgsrv/commit/3809c70dc91003fa6fe6fc24aba59d9dc0c758d9))
* **incus:** serve Simple Streams metadata ([#40](https://github.com/meigma/imgsrv/issues/40)) ([95c67db](https://github.com/meigma/imgsrv/commit/95c67dbf1b2ab8bf86a0386edada515ef5421a60))
* **jobs:** add cas promotion runner ([#17](https://github.com/meigma/imgsrv/issues/17)) ([29b88ec](https://github.com/meigma/imgsrv/commit/29b88ec036cacd0d525f888165a639c7b228da1e))
* **objectstore:** add s3 storage foundation ([#12](https://github.com/meigma/imgsrv/issues/12)) ([fe03bfb](https://github.com/meigma/imgsrv/commit/fe03bfb3e0b8c46cd24adf2264c5e563f405bae7))
* **publish:** add durable publish jobs ([#41](https://github.com/meigma/imgsrv/issues/41)) ([2c96205](https://github.com/meigma/imgsrv/commit/2c96205c3c548008cf9baf7ddf149e7ef0dec3f8))
* **publish:** add manual publish retry ([#42](https://github.com/meigma/imgsrv/issues/42)) ([678557b](https://github.com/meigma/imgsrv/commit/678557bc1ac87045ac67fa00b7598b1112aa5d69))
* **store:** add database port adapters ([#11](https://github.com/meigma/imgsrv/issues/11)) ([8b60ea0](https://github.com/meigma/imgsrv/commit/8b60ea0c7a350b4f0dcedbed7c0d1f85bd2713da))
* **store:** add postgres schema foundation ([#10](https://github.com/meigma/imgsrv/issues/10)) ([6833c71](https://github.com/meigma/imgsrv/commit/6833c7159fa13dedda4acae5dd4a8b8dad63819a))
* **test:** add public integration harness ([#19](https://github.com/meigma/imgsrv/issues/19)) ([bfa3416](https://github.com/meigma/imgsrv/commit/bfa341641d03b1cf4280c5b2cb7094a7ca9fedcb))
* **uploads:** add staging service orchestration ([#13](https://github.com/meigma/imgsrv/issues/13)) ([bc84490](https://github.com/meigma/imgsrv/commit/bc84490e8e8795e84aa9978818a1699d6608b412))


### Bug Fixes

* **catalog:** accept raw.gz artifacts ([#30](https://github.com/meigma/imgsrv/issues/30)) ([4a67655](https://github.com/meigma/imgsrv/commit/4a67655d031d63916a1dee2ee5b2585b8d93d973))
* **store:** block direct published version inserts ([#27](https://github.com/meigma/imgsrv/issues/27)) ([b7b0311](https://github.com/meigma/imgsrv/commit/b7b03111f3872200aa7f3a62dd193c1c94ff014b))

## 0.1.0 (2026-05-13)


### Features

* add demo compose stack ([#56](https://github.com/meigma/imgsrv/issues/56)) ([46df21e](https://github.com/meigma/imgsrv/commit/46df21ef6be7e300a196415a0199490e58b17c29))
* add imgsrv service foundation ([#8](https://github.com/meigma/imgsrv/issues/8)) ([5d41e3d](https://github.com/meigma/imgsrv/commit/5d41e3d0b75dc92cd72d50172a8734f8052596ea))
* add observability foundation ([#9](https://github.com/meigma/imgsrv/issues/9)) ([89fad8d](https://github.com/meigma/imgsrv/commit/89fad8db8cec6dc464df0e3a60f9a05676a8cf3a))
* **api:** add abort upload endpoint ([#20](https://github.com/meigma/imgsrv/issues/20)) ([67e6b45](https://github.com/meigma/imgsrv/commit/67e6b4520396fd045608959d35492d00b68485a9))
* **api:** add CAS blob downloads and upload short-circuit ([#23](https://github.com/meigma/imgsrv/issues/23)) ([2e543c6](https://github.com/meigma/imgsrv/commit/2e543c66ea180fe77dd7b411bf74781a33702bc5))
* **api:** add draft release catalog flow ([#22](https://github.com/meigma/imgsrv/issues/22)) ([c07657c](https://github.com/meigma/imgsrv/commit/c07657cbf073908222a5195fac095b1fd76aa41c))
* **api:** add image alias API ([#24](https://github.com/meigma/imgsrv/issues/24)) ([2dd3131](https://github.com/meigma/imgsrv/commit/2dd3131c2957488b011ca0857bdbd0d120adb85d))
* **api:** add public catalog browse and draft deletes ([#25](https://github.com/meigma/imgsrv/issues/25)) ([3a772f6](https://github.com/meigma/imgsrv/commit/3a772f6700eece8c880f2e6c05baa8f1c4009575))
* **api:** add write auth and artifact downloads ([#26](https://github.com/meigma/imgsrv/issues/26)) ([f5f4fb7](https://github.com/meigma/imgsrv/commit/f5f4fb7daa3c6ad55ebf174c095b2b92eea1ca1b))
* **auth:** add action-based policy foundation ([#29](https://github.com/meigma/imgsrv/issues/29)) ([66bd6d1](https://github.com/meigma/imgsrv/commit/66bd6d11d3f51bbb3d1a7e0a7cdcb7abe43d8eec))
* **auth:** add admin auth management API ([#37](https://github.com/meigma/imgsrv/issues/37)) ([db33775](https://github.com/meigma/imgsrv/commit/db33775b628659bb0872bc6aa0ad8ba368c5d832))
* **auth:** add GitHub Actions OIDC publisher auth ([#32](https://github.com/meigma/imgsrv/issues/32)) ([6ef7f3b](https://github.com/meigma/imgsrv/commit/6ef7f3be1f588fecc8b8d39173fd275da9a254d2))
* **auth:** add OIDC JWT bearer authentication ([#31](https://github.com/meigma/imgsrv/issues/31)) ([f68f6fa](https://github.com/meigma/imgsrv/commit/f68f6fa936e9fda89226aaa58c97e9f9bedf21e5))
* **auth:** add OIDC provisioning rule reconciliation ([#39](https://github.com/meigma/imgsrv/issues/39)) ([92d0bcf](https://github.com/meigma/imgsrv/commit/92d0bcf601fbebc8985ae2d052f9fbda320ff105))
* **auth:** manage OIDC provisioning rules ([#36](https://github.com/meigma/imgsrv/issues/36)) ([bedf24c](https://github.com/meigma/imgsrv/commit/bedf24c63f442edea5a76e1584b5ef2ed76b049e))
* **cas:** implement staged upload commit ([#14](https://github.com/meigma/imgsrv/issues/14)) ([ebe72c3](https://github.com/meigma/imgsrv/commit/ebe72c35d2631b2c2eadf4505c2594fda821089e))
* **client:** add upload SDK ([#18](https://github.com/meigma/imgsrv/issues/18)) ([784ee5f](https://github.com/meigma/imgsrv/commit/784ee5f267666d218a70ec65170a44592f697f41))
* **httpapi:** add upload staging endpoints ([#15](https://github.com/meigma/imgsrv/issues/15)) ([3809c70](https://github.com/meigma/imgsrv/commit/3809c70dc91003fa6fe6fc24aba59d9dc0c758d9))
* **incus:** serve Simple Streams metadata ([#40](https://github.com/meigma/imgsrv/issues/40)) ([95c67db](https://github.com/meigma/imgsrv/commit/95c67dbf1b2ab8bf86a0386edada515ef5421a60))
* **jobs:** add cas promotion runner ([#17](https://github.com/meigma/imgsrv/issues/17)) ([29b88ec](https://github.com/meigma/imgsrv/commit/29b88ec036cacd0d525f888165a639c7b228da1e))
* **objectstore:** add s3 storage foundation ([#12](https://github.com/meigma/imgsrv/issues/12)) ([fe03bfb](https://github.com/meigma/imgsrv/commit/fe03bfb3e0b8c46cd24adf2264c5e563f405bae7))
* **publish:** add durable publish jobs ([#41](https://github.com/meigma/imgsrv/issues/41)) ([2c96205](https://github.com/meigma/imgsrv/commit/2c96205c3c548008cf9baf7ddf149e7ef0dec3f8))
* **publish:** add manual publish retry ([#42](https://github.com/meigma/imgsrv/issues/42)) ([678557b](https://github.com/meigma/imgsrv/commit/678557bc1ac87045ac67fa00b7598b1112aa5d69))
* **store:** add database port adapters ([#11](https://github.com/meigma/imgsrv/issues/11)) ([8b60ea0](https://github.com/meigma/imgsrv/commit/8b60ea0c7a350b4f0dcedbed7c0d1f85bd2713da))
* **store:** add postgres schema foundation ([#10](https://github.com/meigma/imgsrv/issues/10)) ([6833c71](https://github.com/meigma/imgsrv/commit/6833c7159fa13dedda4acae5dd4a8b8dad63819a))
* **test:** add public integration harness ([#19](https://github.com/meigma/imgsrv/issues/19)) ([bfa3416](https://github.com/meigma/imgsrv/commit/bfa341641d03b1cf4280c5b2cb7094a7ca9fedcb))
* **uploads:** add staging service orchestration ([#13](https://github.com/meigma/imgsrv/issues/13)) ([bc84490](https://github.com/meigma/imgsrv/commit/bc84490e8e8795e84aa9978818a1699d6608b412))


### Bug Fixes

* **catalog:** accept raw.gz artifacts ([#30](https://github.com/meigma/imgsrv/issues/30)) ([4a67655](https://github.com/meigma/imgsrv/commit/4a67655d031d63916a1dee2ee5b2585b8d93d973))
* **store:** block direct published version inserts ([#27](https://github.com/meigma/imgsrv/issues/27)) ([b7b0311](https://github.com/meigma/imgsrv/commit/b7b03111f3872200aa7f3a62dd193c1c94ff014b))
