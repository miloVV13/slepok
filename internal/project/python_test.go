package project

import "testing"

func TestPythonRequirementsRequireExactVersions(t *testing.T) {
	for _, s := range []string{"Jinja2==3.1.6", "demo[extra]==1.2.0rc1", "demo==1!2.0+local"} {
		if !pinnedPythonRequirement.MatchString(s) {
			t.Fatalf("rejected %s", s)
		}
	}
	for _, s := range []string{"demo", "demo>=1", "demo==1.*", "--index-url=https://evil", "demo @ https://evil/demo.whl", "demo==1; sys_platform=='win32'"} {
		if pinnedPythonRequirement.MatchString(s) {
			t.Fatalf("accepted %s", s)
		}
	}
	p := &Project{Config: Config{Python: PythonConfig{Requirements: []string{"demo==1"}}}, Lock: &Lock{}}
	if p.CheckPythonLock() == nil {
		t.Fatal("stale Python environment accepted")
	}
	p.Lock.PythonRequirements = []string{"demo==1"}
	if err := p.CheckPythonLock(); err != nil {
		t.Fatal(err)
	}
}
