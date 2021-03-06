// Copyright (c) 2014 Ashley Jeffs
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package output

import (
	"errors"
	"fmt"

	"github.com/Jeffail/benthos/lib/broker"
	"github.com/Jeffail/benthos/lib/log"
	"github.com/Jeffail/benthos/lib/metrics"
	"github.com/Jeffail/benthos/lib/types"
)

//------------------------------------------------------------------------------

var (
	// ErrBrokerNoOutputs is returned when creating a Broker type with zero
	// outputs.
	ErrBrokerNoOutputs = errors.New("attempting to create broker output type with no outputs")
)

//------------------------------------------------------------------------------

func init() {
	Constructors[TypeBroker] = TypeSpec{
		brokerConstructor: NewBroker,
		description: `
The broker output type allows you to configure multiple output targets by
listing them:

` + "``` yaml" + `
output:
  type: broker
  broker:
    pattern: fan_out
    outputs:
    - type: foo
      foo:
        foo_field_1: value1
    - type: bar
      bar:
        bar_field_1: value2
        bar_field_2: value3
    - type: baz
      baz:
        baz_field_1: value4
      processors:
      - type: baz_processor
  processors:
  - type: some_processor
` + "```" + `

The broker pattern determines the way in which messages are allocated to outputs
and can be chosen from the following:

#### ` + "`fan_out`" + `

With the fan out pattern all outputs will be sent every message that passes
through Benthos. If an output applies back pressure it will block all subsequent
messages, and if an output fails to send a message it will be retried
continuously until completion or service shut down.

#### ` + "`round_robin`" + `

With the round robin pattern each message will be assigned a single output
following their order. If an output applies back pressure it will block all
subsequent messages. If an output fails to send a message then the message will
be re-attempted with the next input, and so on.

#### ` + "`greedy`" + `

The greedy pattern results in higher output throughput at the cost of
potentially disproportionate message allocations to those outputs. Each message
is sent to a single output, which is determined by allowing outputs to claim
messages as soon as they are able to process them. This results in certain
faster outputs potentially processing more messages at the cost of slower
outputs.

#### ` + "`try`" + `

The try pattern attempts to send each message to only one output, starting from
the first output on the list. If an output attempt fails then the broker
attempts to send to the next output in the list and so on.

This pattern is useful for triggering events in the case where certain output
targets have broken. For example, if you had an output type ` + "`http_client`" + `
but wished to reroute messages whenever the endpoint becomes unreachable you
could use a try broker.

### Utilising More Outputs

When using brokered outputs with patterns such as round robin or greedy it is
possible to have multiple messages in-flight at the same time. In order to fully
utilise this you either need to have a greater number of input sources than
output sources [or use a buffer](../buffers/README.md).

### Processors

It is possible to configure [processors](../processors/README.md) at the broker
level, where they will be applied to _all_ child outputs, as well as on the
individual child outputs. If you have processors at both the broker level _and_
on child outputs then the broker processors will be applied _before_ the child
nodes processors.`,
		sanitiseConfigFunc: func(conf Config) (interface{}, error) {
			nestedOutputs := conf.Broker.Outputs
			outSlice := []interface{}{}
			for _, output := range nestedOutputs {
				sanOutput, err := SanitiseConfig(output)
				if err != nil {
					return nil, err
				}
				outSlice = append(outSlice, sanOutput)
			}
			return map[string]interface{}{
				"copies":  conf.Broker.Copies,
				"pattern": conf.Broker.Pattern,
				"outputs": outSlice,
			}, nil
		},
	}
}

//------------------------------------------------------------------------------

// BrokerConfig contains configuration fields for the Broker output type.
type BrokerConfig struct {
	Copies  int              `json:"copies" yaml:"copies"`
	Pattern string           `json:"pattern" yaml:"pattern"`
	Outputs brokerOutputList `json:"outputs" yaml:"outputs"`
}

// NewBrokerConfig creates a new BrokerConfig with default values.
func NewBrokerConfig() BrokerConfig {
	return BrokerConfig{
		Copies:  1,
		Pattern: "fan_out",
		Outputs: brokerOutputList{},
	}
}

//------------------------------------------------------------------------------

// NewBroker creates a new Broker output type. Messages will be sent out to the
// list of outputs according to the chosen broker pattern.
func NewBroker(
	conf Config,
	mgr types.Manager,
	log log.Modular,
	stats metrics.Type,
	pipelines ...types.PipelineConstructorFunc,
) (Type, error) {
	outputConfs := conf.Broker.Outputs

	lOutputs := len(outputConfs) * conf.Broker.Copies

	if lOutputs <= 0 {
		return nil, ErrBrokerNoOutputs
	}
	if lOutputs == 1 {
		return New(outputConfs[0], mgr, log, stats, pipelines...)
	}

	outputs := make([]types.Output, lOutputs)

	var err error
	for j := 0; j < conf.Broker.Copies; j++ {
		for i, oConf := range outputConfs {
			ns := fmt.Sprintf("broker.outputs.%v", (j*conf.Broker.Copies)+i)
			outputs[j*len(outputConfs)+i], err = New(
				oConf, mgr,
				log.NewModule("."+ns),
				metrics.Combine(stats, metrics.Namespaced(stats, ns)),
				pipelines...)
			if err != nil {
				return nil, err
			}
		}
	}

	switch conf.Broker.Pattern {
	case "fan_out":
		return broker.NewFanOut(outputs, log, stats)
	case "round_robin":
		return broker.NewRoundRobin(outputs, stats)
	case "greedy":
		return broker.NewGreedy(outputs)
	case "try":
		return broker.NewTry(outputs, stats)
	}

	return nil, fmt.Errorf("broker pattern was not recognised: %v", conf.Broker.Pattern)
}

//------------------------------------------------------------------------------
