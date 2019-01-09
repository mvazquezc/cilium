// +build !ignore_autogenerated

// Copyright 2017-2018 Authors of Cilium
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Code generated by deepcopy-gen. DO NOT EDIT.

package models

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *MessageForwardingStatistics) DeepCopyInto(out *MessageForwardingStatistics) {
	*out = *in
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new MessageForwardingStatistics.
func (in *MessageForwardingStatistics) DeepCopy() *MessageForwardingStatistics {
	if in == nil {
		return nil
	}
	out := new(MessageForwardingStatistics)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ProxyStatistics) DeepCopyInto(out *ProxyStatistics) {
	*out = *in
	if in.Statistics != nil {
		in, out := &in.Statistics, &out.Statistics
		*out = new(RequestResponseStatistics)
		(*in).DeepCopyInto(*out)
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ProxyStatistics.
func (in *ProxyStatistics) DeepCopy() *ProxyStatistics {
	if in == nil {
		return nil
	}
	out := new(ProxyStatistics)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RequestResponseStatistics) DeepCopyInto(out *RequestResponseStatistics) {
	*out = *in
	if in.Requests != nil {
		in, out := &in.Requests, &out.Requests
		*out = new(MessageForwardingStatistics)
		**out = **in
	}
	if in.Responses != nil {
		in, out := &in.Responses, &out.Responses
		*out = new(MessageForwardingStatistics)
		**out = **in
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RequestResponseStatistics.
func (in *RequestResponseStatistics) DeepCopy() *RequestResponseStatistics {
	if in == nil {
		return nil
	}
	out := new(RequestResponseStatistics)
	in.DeepCopyInto(out)
	return out
}