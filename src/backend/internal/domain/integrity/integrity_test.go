package integrity

import (
	"strings"
	"testing"

	"moodle-autograder/internal/domain/work"
)

const service = `public class GreeterService : Greeter.GreeterBase
{
    private readonly ILogger<GreeterService> logger;
    public GreeterService(ILogger<GreeterService> logger) => this.logger = logger;
    public override Task<HelloReply> SayHello(HelloRequest request, ServerCallContext context)
    {
        logger.LogInformation("SayHello for {Name}", request.Name);
        if (string.IsNullOrWhiteSpace(request.Name)) throw new RpcException(new Status(StatusCode.InvalidArgument, "name is required"));
        return Task.FromResult(new HelloReply { Message = $"Hello, {request.Name}!" });
    }
}`

const other = `using var channel = GrpcChannel.ForAddress("https://localhost:7001");
var client = new Calculator.CalculatorClient(channel);
foreach (var (a, b) in new[] { (2, 3), (10, -4) }) { var sum = await client.AddAsync(new Pair { A = a, B = b }); Console.WriteLine(sum.Value); }`

// thresholds are the default course settings.
var thresholds = Thresholds{Similar: 0.5, Copy: 0.75, CopyFiles: 2}

func snapshot(t *testing.T, code, report string) work.Snapshot {
	t.Helper()
	return work.Snapshot{Code: []work.CodeFile{{Path: "Lab/Service.cs", Text: code}}, Report: report}
}

func TestCopiesAreFoundOriginalsAreNot(t *testing.T) {
	names := []string{"Ivan Petrov", "Anna Smirnova", "Maria Popova", "Oleg Kuznetsov"}
	report := "Student: %s\nI implemented the greeter service with unary and streaming calls and tested it with grpcurl.\nConclusion: gRPC gives a strict contract and fast transport."
	ivan := Extract(snapshot(t, service, strings.Replace(report, "%s", "Ivan Petrov", 1)), names, "")
	anna := Extract(snapshot(t, service, strings.Replace(report, "%s", "Anna Smirnova", 1)), names, "")
	maria := Extract(snapshot(t, strings.NewReplacer("GreeterService", "HelloService", "request", "req", "logger", "log").Replace(service), "My own report text about something else entirely here."), names, "")
	oleg := Extract(snapshot(t, other, "A calculator over gRPC with Add and Multiply, errors become RpcException."), names, "")

	if m, ok := Compare(ivan, anna, thresholds); !ok || m.Severity != "high" || m.IdenticalFiles != 1 || m.ReportSimilarity < 0.9 {
		t.Errorf("exact copy with changed name: %+v ok=%v", m, ok)
	}

	if m, ok := Compare(ivan, maria, thresholds); !ok || m.Severity != "high" || m.CodeSimilarity < 0.8 {
		t.Errorf("renamed identifiers must still match: %+v ok=%v", m, ok)
	}

	if m, ok := Compare(ivan, oleg, thresholds); ok {
		t.Errorf("independent work flagged: %+v", m)
	}
}
