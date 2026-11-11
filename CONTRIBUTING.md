# Notice to external contributors

## General info

Hello! In order for us (YANDEX LLC) to accept patches and other contributions from you, you will have to adopt our Yandex Contributor License Agreement (the **CLA**). The current version of the CLA can be found here:
1) https://yandex.ru/legal/cla/?lang=en (in English) and 
2) https://yandex.ru/legal/cla/?lang=ru (in Russian).

By adopting the CLA, you state the following:

* You obviously wish and are willingly licensing your contributions to us for our open source projects under the terms of the CLA,
* You have read the terms and conditions of the CLA and agree with them in full,
* You are legally able to provide and license your contributions as stated,
* We may use your contributions for our open source projects and for any other our project too,
* We rely on your assurances concerning the rights of third parties in relation to your contributions.

If you agree with these principles, please read and adopt our CLA. By providing us your contributions, you hereby declare that you have already read and adopt our CLA, and we may freely merge your contributions with our corresponding open source project and use it in further in accordance with terms and conditions of the CLA.

## Provide contributions 

If you have already adopted terms and conditions of the CLA, you are able to provide your contributions. When you submit your pull request, please add the following information into it:

```
I hereby agree to the terms of the CLA available at: [link].
```

Replace the bracketed text as follows:
* [link] is the link to the current version of the CLA: https://yandex.ru/legal/cla/?lang=en (in English) or https://yandex.ru/legal/cla/?lang=ru (in Russian).

It is enough to provide us such notification once. 

## Development

Run the full gate before submitting:

```sh
test -z "$(gofmt -l .)" && go vet ./... && go test -race -count=1 ./... \
  && golangci-lint run ./... \
  && go run github.com/campoy/embedmd@v1.0.0 -d README.md docs/GUIDE.md
```

Tests include regressions, randomized registration and operation sequences,
lifecycle models, concurrent race checks, and fuzz targets. For changes to
resolution or teardown, also run:

```sh
go test -race -run '^$' -fuzz 'FuzzMachine$' -fuzztime 2m .
go test -race -run '^$' -fuzz FuzzMachineConcurrent -fuzztime 2m .
```

Examples are a separate module:

```sh
cd examples && go test -race -count=1 ./...
```

Examples in `README.md` and `docs/GUIDE.md` are embedded from `examples/` with
[embedmd](https://github.com/campoy/embedmd). After editing an example, run
from the repository root:

```sh
gofmt -w examples/ && go run github.com/campoy/embedmd@v1.0.0 -w README.md docs/GUIDE.md
```

## Other questions

If you have any questions, please mail us at opensource-support@yandex-team.ru.
